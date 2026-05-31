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
