package indexer

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
)

// TestIndexFileResolvesCrossFile verifies the incremental path resolves a
// receiver-typed method call to a definition in ANOTHER file — proving the
// resolver is built from the full functions table, not just the changed file.
func TestIndexFileResolvesCrossFile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	// engine.go defines the Engine type and its Stop method.
	writeFile(t, root, "engine.go", `package app

type Engine struct{}

func (e *Engine) Stop() {}
`)
	// caller.go (separate file) calls e.Stop() on an *Engine parameter.
	writeFile(t, root, "caller.go", `package app

func Drive(e *Engine) {
	e.Stop()
}
`)

	database, err := db.Open(filepath.Join(root, ".max-context", "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		t.Fatalf("PrepareQueries: %v", err)
	}
	defer q.Close()

	// Full index first.
	if err := Index(ctx, root, database, q); err != nil {
		t.Fatalf("Index: %v", err)
	}
	assertStopResolved(t, database, "full index")

	// Now touch caller.go incrementally; resolution must still find Engine.Stop
	// in engine.go via the full functions table.
	if err := IndexFile(ctx, root, "caller.go", database, q); err != nil {
		t.Fatalf("IndexFile: %v", err)
	}
	assertStopResolved(t, database, "incremental reindex")
}

// TestIndexFileReindexesCalleeFile reindexes a file whose functions are
// referenced as callee_id by call edges in OTHER files. The cross-file edge
// caller.go:Drive -> engine.go:Stop survives the `DELETE FROM calls WHERE
// file_path='engine.go'` step (it lives in caller.go), so the subsequent
// `DELETE FROM functions WHERE file_path='engine.go'` removes a row that is
// still referenced by calls.callee_id.
//
// Failure mode (pre-fix, foreign_keys=ON): IndexFile's DELETE FROM functions
// fails with "FOREIGN KEY constraint failed", the transaction rolls back, and
// the edit silently never reaches the index — so widely-called files become
// un-updatable. RunWorker compounds this by discarding the error.
func TestIndexFileReindexesCalleeFile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	// engine.go defines Engine and its Stop method; caller.go calls e.Stop().
	writeFile(t, root, "engine.go", `package app

type Engine struct{}

func (e *Engine) Stop() {}
`)
	writeFile(t, root, "caller.go", `package app

func Drive(e *Engine) {
	e.Stop()
}
`)

	database, q := openIndexDB(t, root)
	defer database.Close()
	defer q.Close()

	if err := Index(ctx, root, database, q); err != nil {
		t.Fatalf("Index: %v", err)
	}
	assertStopResolved(t, database, "full index")

	// Edit engine.go (the CALLEE file): add a new method Halt. Reindexing it must
	// not trip the FK from caller.go's surviving Drive -> Stop edge.
	writeFile(t, root, "engine.go", `package app

type Engine struct{}

func (e *Engine) Stop() {}

func (e *Engine) Halt() {}
`)
	if err := IndexFile(ctx, root, "engine.go", database, q); err != nil {
		t.Fatalf("IndexFile(engine.go): %v", err)
	}

	// Halt landed.
	var halt int
	if err := database.QueryRow(`SELECT COUNT(*) FROM functions WHERE name='Halt' AND file_path='engine.go'`).Scan(&halt); err != nil {
		t.Fatalf("count Halt: %v", err)
	}
	if halt != 1 {
		t.Errorf("Halt rows = %d, want 1 (reindex did not land)", halt)
	}

	// The cross-file edge still resolves, now to the NEW engine.go Stop row.
	assertStopResolved(t, database, "callee reindex")
}

// TestIndexFileCalleeRenamed renames the cross-file-referenced method
// (Stop -> Halt) in the callee file and reindexes it. The surviving
// caller.go:Drive -> Stop edge must become unresolved: NOT left pointing at the
// deleted Stop id (a dangling reference), and NOT still claiming receiver-typed
// confidence for a target that no longer exists.
func TestIndexFileCalleeRenamed(t *testing.T) {
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

	database, q := openIndexDB(t, root)
	defer database.Close()
	defer q.Close()

	if err := Index(ctx, root, database, q); err != nil {
		t.Fatalf("Index: %v", err)
	}
	assertStopResolved(t, database, "full index")

	// Rename Stop -> Halt and reindex the callee file.
	writeFile(t, root, "engine.go", `package app

type Engine struct{}

func (e *Engine) Halt() {}
`)
	if err := IndexFile(ctx, root, "engine.go", database, q); err != nil {
		t.Fatalf("IndexFile(engine.go): %v", err)
	}

	assertStopUnresolved(t, database, "callee renamed")
}

// TestIndexFileCalleeDeleted removes the callee file from disk and reindexes its
// path (the watcher emits the path on Remove just as it does on Write). This must
// not trip an FK error, must remove the file's symbols, and must leave the
// cross-file edge unresolved with its stale marker cleared.
func TestIndexFileCalleeDeleted(t *testing.T) {
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

	database, q := openIndexDB(t, root)
	defer database.Close()
	defer q.Close()

	if err := Index(ctx, root, database, q); err != nil {
		t.Fatalf("Index: %v", err)
	}
	assertStopResolved(t, database, "full index")

	// Delete engine.go on disk, then drive the watcher's delete path: the same
	// IndexFile(path) call it makes for any change event.
	if err := os.Remove(filepath.Join(root, "engine.go")); err != nil {
		t.Fatalf("remove engine.go: %v", err)
	}
	if err := IndexFile(ctx, root, "engine.go", database, q); err != nil {
		t.Fatalf("IndexFile(engine.go) on delete: %v", err)
	}

	// engine.go's symbols are gone.
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM functions WHERE file_path='engine.go'`).Scan(&n); err != nil {
		t.Fatalf("count engine.go funcs: %v", err)
	}
	if n != 0 {
		t.Errorf("engine.go functions = %d, want 0 (delete did not land)", n)
	}

	assertStopUnresolved(t, database, "callee deleted")
}

// assertStopUnresolved checks that the Drive -> Stop edge is unresolved: no
// callee_id, no leftover 'stale' marker, and not still 'receiver-typed'.
func assertStopUnresolved(t *testing.T, database *sql.DB, when string) {
	t.Helper()
	var resolution string
	var calleeID sql.NullInt64
	err := database.QueryRow(`
		SELECT e.resolution, e.callee_id
		FROM calls e
		JOIN functions caller ON caller.id = e.caller_id
		WHERE caller.name = 'Drive' AND e.callee_name = 'Stop'`).Scan(&resolution, &calleeID)
	if err != nil {
		t.Fatalf("[%s] query Drive->Stop edge: %v", when, err)
	}
	if calleeID.Valid {
		t.Errorf("[%s] callee_id = %d, want NULL (dangling reference)", when, calleeID.Int64)
	}
	if resolution != resUnresolved {
		t.Errorf("[%s] resolution = %q, want %q", when, resolution, resUnresolved)
	}
}

// openIndexDB opens + migrates a fresh index DB under root and prepares queries.
func openIndexDB(t *testing.T, root string) (*sql.DB, *db.Queries) {
	t.Helper()
	database, err := db.Open(filepath.Join(root, ".max-context", "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		t.Fatalf("PrepareQueries: %v", err)
	}
	return database, q
}

// assertStopResolved checks that the e.Stop() edge resolves, with receiver-typed
// confidence, to the Engine.Stop definition in engine.go.
func assertStopResolved(t *testing.T, database *sql.DB, when string) {
	t.Helper()
	var resolution string
	var targetFile sql.NullString
	err := database.QueryRow(`
		SELECT e.resolution, tf.file_path
		FROM calls e
		JOIN functions caller ON caller.id = e.caller_id
		LEFT JOIN functions tf ON tf.id = e.callee_id
		WHERE caller.name = 'Drive' AND e.callee_name = 'Stop'`).Scan(&resolution, &targetFile)
	if err != nil {
		t.Fatalf("[%s] query e.Stop edge: %v", when, err)
	}
	if resolution != resReceiverTyped {
		t.Errorf("[%s] resolution = %q, want %q", when, resolution, resReceiverTyped)
	}
	if !targetFile.Valid || targetFile.String != "engine.go" {
		t.Errorf("[%s] target file = %v, want engine.go", when, targetFile.String)
	}
}

// writeFile writes content to root/rel, creating parent dirs.
func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
