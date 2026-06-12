package db

import (
	"path/filepath"
	"testing"
)

// TestMigrationV8Documents verifies a fresh migrate reaches v8 with the
// documents table and its self-maintaining FTS index: insert/update/delete
// must keep documents_fts consistent via the triggers (the external-content
// 'delete' command form), with no manual rebuild.
func TestMigrationV8Documents(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "v8.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	if err := Migrate(database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if v, _ := getSchemaVersion(database); v != schemaVersion {
		t.Fatalf("schema version = %d, want %d", v, schemaVersion)
	}

	if _, err := database.Exec(
		`INSERT INTO documents (file_path, title, kind, content) VALUES (?,?,?,?)`,
		"docs/setup.md", "Setup Guide", "markdown", "Install the watcher daemon and run the indexer."); err != nil {
		t.Fatalf("insert document: %v", err)
	}

	count := func(match string) int {
		t.Helper()
		var n int
		if err := database.QueryRow(
			`SELECT COUNT(*) FROM documents_fts WHERE documents_fts MATCH ?`, match).Scan(&n); err != nil {
			t.Fatalf("fts query %q: %v", match, err)
		}
		return n
	}

	if n := count(`"watcher"`); n != 1 {
		t.Errorf("FTS after insert = %d rows, want 1", n)
	}

	if _, err := database.Exec(
		`UPDATE documents SET content = 'Completely rewritten body.' WHERE file_path = ?`,
		"docs/setup.md"); err != nil {
		t.Fatalf("update document: %v", err)
	}
	if n := count(`"watcher"`); n != 0 {
		t.Errorf("FTS still matches old content after update (%d rows)", n)
	}
	if n := count(`"rewritten"`); n != 1 {
		t.Errorf("FTS misses new content after update (%d rows)", n)
	}

	if _, err := database.Exec(`DELETE FROM documents WHERE file_path = ?`, "docs/setup.md"); err != nil {
		t.Fatalf("delete document: %v", err)
	}
	if n := count(`"rewritten"`); n != 0 {
		t.Errorf("FTS still matches after delete (%d rows)", n)
	}
}
