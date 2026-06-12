package indexer

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// callGraphSnapshot returns a stable, id-independent view of the call graph:
// (caller file, caller name, callee name, resolved-callee file, resolution).
// Callee FILE (not id) is used so a delta-built graph and a full-rebuild graph —
// which assign different autoincrement ids — are comparable by semantics.
func callGraphSnapshot(t *testing.T, database *sql.DB) []string {
	t.Helper()
	rows, err := database.Query(`
		SELECT caller.file_path, caller.name, c.callee_name,
		       COALESCE(tf.file_path, ''), c.resolution
		FROM calls c
		JOIN functions caller ON caller.id = c.caller_id
		LEFT JOIN functions tf ON tf.id = c.callee_id
		ORDER BY 1,2,3,4,5`)
	if err != nil {
		t.Fatalf("snapshot query: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var cf, cn, calleeName, tf, res string
		if err := rows.Scan(&cf, &cn, &calleeName, &tf, &res); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, fmt.Sprintf("%s|%s -> %s @%s [%s]", cf, cn, calleeName, tf, res))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// TestResolverCacheEquivalentToFullReindex is the Phase 4 safety net: a sequence
// of edits applied through the cached incremental path (worker semantics) must
// yield exactly the same call graph as a single full reindex of the identical
// final on-disk state. This proves the delta-maintained resolver (removeFile +
// addFileFromDB) stays equivalent to a fresh full scan across adds, edits,
// cross-file callee renames, and deletes.
func TestResolverCacheEquivalentToFullReindex(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeFile(t, root, "engine.go", `package app

type Engine struct{}

func (e *Engine) Stop() {}
`)
	writeFile(t, root, "caller.go", `package app

func Drive(e *Engine) {
	e.Stop()
}
`)
	writeFile(t, root, "util.go", `package app

func Helper(e *Engine) {
	Drive(e)
}
`)

	database, q := openIndexDB(t, root)
	defer database.Close()
	defer q.Close()

	if err := Index(ctx, root, database, q); err != nil {
		t.Fatalf("initial Index: %v", err)
	}

	cache := NewResolverCache()
	apply := func(rel, content string) {
		if content == "" {
			if err := os.Remove(filepath.Join(root, rel)); err != nil {
				t.Fatalf("remove %s: %v", rel, err)
			}
		} else {
			writeFile(t, root, rel, content)
		}
		if err := IndexFile(ctx, root, rel, database, q, cache); err != nil {
			t.Fatalf("IndexFile(%s): %v", rel, err)
		}
	}

	// Edit the callee file: add a method (callee-file reindex with surviving
	// cross-file edge — exercises stale re-resolution + delta add).
	apply("engine.go", `package app

type Engine struct{}

func (e *Engine) Stop() {}

func (e *Engine) Halt() {}
`)
	// Edit a caller to call the new method (delta add of a new edge).
	apply("caller.go", `package app

func Drive(e *Engine) {
	e.Stop()
	e.Halt()
}
`)
	// Add a brand-new file with a cross-file method call (delta add of a file).
	apply("more.go", `package app

func More(e *Engine) {
	e.Halt()
}
`)
	// Delete a file (delta remove; its same-package edge Helper->Drive disappears).
	apply("util.go", "")
	// Rename a widely-called method: Stop -> Resume. Drive->Stop must become
	// unresolved in BOTH the incremental and the full graph.
	apply("engine.go", `package app

type Engine struct{}

func (e *Engine) Resume() {}

func (e *Engine) Halt() {}
`)

	incremental := callGraphSnapshot(t, database)

	// Full reindex of the identical final disk state.
	if err := Index(ctx, root, database, q); err != nil {
		t.Fatalf("final full Index: %v", err)
	}
	full := callGraphSnapshot(t, database)

	if !reflect.DeepEqual(incremental, full) {
		t.Errorf("incremental call graph != full reindex\nincremental:\n%s\n\nfull:\n%s",
			joinLines(incremental), joinLines(full))
	}
}

// TestResolverCacheInterfaceDispatchEquivalent checks that the low-confidence
// interface-dispatch edges produced incrementally (cached path) match a full
// reindex — so the delta resolver's interface/implements bookkeeping stays in
// sync across edits to interfaces, implementations, and callers.
func TestResolverCacheInterfaceDispatchEquivalent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeFile(t, root, "notifier.go", `package notify

type Notifier interface {
	Send(msg string) error
}
`)
	writeFile(t, root, "email.go", `package notify

type Email struct{}

func (e *Email) Send(msg string) error { return nil }
`)
	writeFile(t, root, "caller.go", `package notify

func Notify(n Notifier) {
	n.Send("hi")
}
`)

	database, q := openIndexDB(t, root)
	defer database.Close()
	defer q.Close()
	if err := Index(ctx, root, database, q); err != nil {
		t.Fatalf("Index: %v", err)
	}

	cache := NewResolverCache()
	apply := func(rel, content string) {
		writeFile(t, root, rel, content)
		if err := IndexFile(ctx, root, rel, database, q, cache); err != nil {
			t.Fatalf("IndexFile(%s): %v", rel, err)
		}
	}

	// Add a second implementation incrementally (delta add of a concrete type that
	// now also satisfies Notifier -> a new interface-dispatch edge must appear).
	apply("sms.go", `package notify

type SMS struct{}

func (s *SMS) Send(msg string) error { return nil }
`)
	// Edit the caller (its interface-dispatch edges must be regenerated to BOTH impls).
	apply("caller.go", `package notify

func Notify(n Notifier) {
	n.Send("hello")
}
`)

	incremental := callGraphSnapshot(t, database)
	if err := Index(ctx, root, database, q); err != nil {
		t.Fatalf("final full Index: %v", err)
	}
	full := callGraphSnapshot(t, database)
	if !reflect.DeepEqual(incremental, full) {
		t.Errorf("incremental interface-dispatch graph != full reindex\nincremental:\n%s\nfull:\n%s",
			joinLines(incremental), joinLines(full))
	}
	// Sanity: there ARE interface-dispatch edges to compare.
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM calls WHERE resolution='interface-dispatch'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Error("expected interface-dispatch edges, found none")
	}
}

func joinLines(s []string) string {
	out := ""
	for _, line := range s {
		out += "  " + line + "\n"
	}
	return out
}
