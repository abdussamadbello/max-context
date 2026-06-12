package db

import (
	"path/filepath"
	"testing"
)

// TestMigrationV7IndexErrors verifies a fresh migrate reaches v7 with the
// index_errors table present and writable (the staleness/health backing store).
func TestMigrationV7IndexErrors(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "v7.db"))
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
		`INSERT INTO index_errors (file_path, error, timestamp) VALUES (?,?,?)`,
		"broken.go", "parse failed", 123); err != nil {
		t.Fatalf("insert index_errors: %v", err)
	}
	// PRIMARY KEY on file_path: a retry replaces rather than duplicates.
	if _, err := database.Exec(
		`INSERT OR REPLACE INTO index_errors (file_path, error, timestamp) VALUES (?,?,?)`,
		"broken.go", "still failing", 456); err != nil {
		t.Fatalf("replace index_errors: %v", err)
	}
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM index_errors`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("index_errors rows = %d, want 1 (file_path is PRIMARY KEY)", n)
	}
}
