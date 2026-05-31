package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// columnExists reports whether table has a column named col.
func columnExists(t *testing.T, db *sql.DB, table, col string) bool {
	t.Helper()
	rows, err := db.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		t.Fatalf("pragma_table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == col {
			return true
		}
	}
	return false
}

// TestMigrationV2FreshDB verifies a fresh Open+Migrate lands at v2 with the new columns.
func TestMigrationV2FreshDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	v, err := getSchemaVersion(db)
	if err != nil {
		t.Fatalf("getSchemaVersion: %v", err)
	}
	if v != schemaVersion {
		t.Fatalf("schema version = %d, want %d", v, schemaVersion)
	}

	for _, c := range []struct{ table, col string }{
		{"calls", "resolution"},
		{"calls", "receiver_name"},
		{"functions", "kind"},
		{"functions", "receiver_type"},
		{"functions", "package"},
	} {
		if !columnExists(t, db, c.table, c.col) {
			t.Errorf("column %s.%s missing after fresh migrate", c.table, c.col)
		}
	}
}

// TestMigrationV2Upgrade simulates an existing v1 DB and verifies the v1->v2
// upgrade adds the new columns and preserves the default resolution marker.
func TestMigrationV2Upgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Build a v1-only database: run migrationV1 directly and stamp version 1.
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := migrationV1(tx); err != nil {
		t.Fatalf("migrationV1: %v", err)
	}
	if err := setSchemaVersion(tx, 1); err != nil {
		t.Fatalf("setSchemaVersion: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Seed a function and a v1-shaped call edge (no resolution column yet).
	if _, err := db.Exec(`INSERT INTO functions (name, file_path, start_line, end_line, language, exported) VALUES ('Foo','a.go',1,2,'go',1)`); err != nil {
		t.Fatalf("seed function: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO calls (caller_id, callee_id, callee_name, file_path, line) VALUES (1, 1, 'Foo', 'a.go', 5)`); err != nil {
		t.Fatalf("seed call: %v", err)
	}

	// Now upgrade to current version.
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate upgrade: %v", err)
	}
	if v, _ := getSchemaVersion(db); v != schemaVersion {
		t.Fatalf("post-upgrade version = %d, want %d", v, schemaVersion)
	}

	// New columns exist...
	for _, c := range []struct{ table, col string }{
		{"calls", "resolution"},
		{"calls", "receiver_name"},
		{"functions", "kind"},
		{"functions", "receiver_type"},
		{"functions", "package"},
	} {
		if !columnExists(t, db, c.table, c.col) {
			t.Errorf("column %s.%s missing after upgrade", c.table, c.col)
		}
	}

	// ...and the pre-existing edge defaults to the legacy semantics.
	var resolution string
	if err := db.QueryRow("SELECT resolution FROM calls WHERE id = 1").Scan(&resolution); err != nil {
		t.Fatalf("read resolution: %v", err)
	}
	if resolution != "name-global" {
		t.Errorf("legacy edge resolution = %q, want %q", resolution, "name-global")
	}
	var kind string
	if err := db.QueryRow("SELECT kind FROM functions WHERE id = 1").Scan(&kind); err != nil {
		t.Fatalf("read kind: %v", err)
	}
	if kind != "func" {
		t.Errorf("legacy function kind = %q, want %q", kind, "func")
	}
}

// TestMigrationV5ClassBases verifies the inheritance table exists after migrate
// and accepts class→base rows.
func TestMigrationV5ClassBases(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir + "/v5.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := Migrate(database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO class_bases (class_name, base_name, package, file_path) VALUES (?,?,?,?)`,
		"Client", "BaseClient", "", "httpx/_client.py"); err != nil {
		t.Fatalf("insert class_bases: %v", err)
	}
	var base string
	if err := database.QueryRow(`SELECT base_name FROM class_bases WHERE class_name='Client'`).Scan(&base); err != nil {
		t.Fatalf("query: %v", err)
	}
	if base != "BaseClient" {
		t.Errorf("base = %q, want BaseClient", base)
	}
}

// TestMigrationV6FTSTriggers verifies the FTS sync triggers keep the
// external-content FTS index consistent with the base tables across INSERT,
// UPDATE, and DELETE — with NO explicit RebuildAllFTS call. This is the atomic,
// race-free replacement for the post-commit full rebuild (the E01 FTS-race fix).
func TestMigrationV6FTSTriggers(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir + "/v6.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := Migrate(database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ftsCount := func(term string) int {
		var n int
		if err := database.QueryRow(
			"SELECT COUNT(*) FROM functions_fts WHERE functions_fts MATCH ?", term).Scan(&n); err != nil {
			t.Fatalf("fts match %q: %v", term, err)
		}
		return n
	}

	// INSERT -> trigger should index it (no manual rebuild).
	res, err := database.Exec(
		`INSERT INTO functions (name, file_path, start_line, end_line, language, exported, code, docstring, signature)
		 VALUES ('handleRequest','h.go',1,10,'go',1,'parse the incoming payload','',' handleRequest()')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if got := ftsCount("payload"); got != 1 {
		t.Fatalf("after INSERT: FTS match 'payload' = %d, want 1 (insert trigger)", got)
	}

	// UPDATE -> old content gone, new content searchable.
	if _, err := database.Exec(`UPDATE functions SET code='render the response body' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if got := ftsCount("payload"); got != 0 {
		t.Errorf("after UPDATE: stale 'payload' still matches (%d); update trigger didn't delete old", got)
	}
	if got := ftsCount("response"); got != 1 {
		t.Errorf("after UPDATE: new 'response' matches = %d, want 1", got)
	}

	// DELETE -> removed from FTS.
	if _, err := database.Exec(`DELETE FROM functions WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if got := ftsCount("response"); got != 0 {
		t.Errorf("after DELETE: 'response' still matches (%d); delete trigger didn't fire", got)
	}
}
