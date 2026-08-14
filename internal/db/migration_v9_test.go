package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrationV9AddsColumns(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "v9.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := Migrate(database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for _, c := range []struct{ table, column string }{
		{"functions", "name_parts"},
		{"types", "name_parts"},
		{"types", "start_line"},
	} {
		if !columnExists(t, database, c.table, c.column) {
			t.Errorf("%s.%s missing after migration", c.table, c.column)
		}
	}
}

// Upgrading an existing index must not require a reindex: name_parts is
// backfilled for rows already present, so multi-word search works immediately.
func TestMigrationV9BackfillsExistingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")

	// Build a database at v8 — before name_parts existed — and seed it.
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for v := 1; v <= 8; v++ {
		if err := runMigration(database, v); err != nil {
			t.Fatalf("migration %d: %v", v, err)
		}
	}
	if _, err := database.Exec(
		`INSERT INTO functions (name, file_path, start_line, end_line, language, exported) VALUES (?, ?, ?, ?, ?, ?)`,
		"ResolverCache", "internal/indexer/resolver.go", 120, 140, "go", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`INSERT INTO types (name, file_path, kind, definition, exported) VALUES (?, ?, ?, ?, ?)`,
		"PaymentProcessor", "billing.go", "class", "class PaymentProcessor:", 1); err != nil {
		t.Fatal(err)
	}
	database.Close()

	// Reopen and migrate the rest of the way.
	database, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := Migrate(database); err != nil {
		t.Fatalf("Migrate to v9: %v", err)
	}

	for _, tc := range []struct{ table, name, want string }{
		{"functions", "ResolverCache", "Resolver Cache"},
		{"types", "PaymentProcessor", "Payment Processor"},
	} {
		var parts sql.NullString
		if err := database.QueryRow("SELECT name_parts FROM "+tc.table+" WHERE name = ?", tc.name).Scan(&parts); err != nil {
			t.Fatalf("read %s.name_parts: %v", tc.table, err)
		}
		if parts.String != tc.want {
			t.Errorf("%s.name_parts = %q, want %q", tc.table, parts.String, tc.want)
		}
	}

	// And the rebuilt FTS index must actually be searchable on the split form.
	var got string
	err = database.QueryRow(
		`SELECT f.name FROM functions_fts JOIN functions f ON f.id = functions_fts.rowid
		 WHERE functions_fts MATCH ? LIMIT 1`, `"resolver" "cache"`).Scan(&got)
	if err != nil {
		t.Fatalf("multi-word FTS query after migration: %v", err)
	}
	if got != "ResolverCache" {
		t.Errorf("FTS returned %q, want ResolverCache", got)
	}
}

// A fresh database and an upgraded one must end up with the same shape.
func TestMigrationV9MatchesFreshSchema(t *testing.T) {
	fresh, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err := Migrate(fresh); err != nil {
		t.Fatal(err)
	}

	var version int
	if err := fresh.QueryRow("SELECT value FROM _meta WHERE key = 'schema_version'").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Errorf("schema_version = %d, want %d", version, schemaVersion)
	}

	// The FTS triggers must survive the drop/recreate in v9, or incremental
	// indexing silently stops updating the search index.
	for _, trigger := range []string{
		"functions_ai", "functions_ad", "functions_au",
		"types_ai", "types_ad", "types_au",
	} {
		var n int
		if err := fresh.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?", trigger).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("trigger %s missing after migration", trigger)
		}
	}
}

// The triggers must keep name_parts in the FTS index on update and delete, or
// an edited file leaves stale search rows behind.
func TestNamePartsStaysConsistentThroughEdits(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "edits.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := Migrate(database); err != nil {
		t.Fatal(err)
	}

	matches := func(query string) int {
		var n int
		if err := database.QueryRow(
			`SELECT COUNT(*) FROM functions_fts WHERE functions_fts MATCH ?`, query).Scan(&n); err != nil {
			t.Fatalf("match %q: %v", query, err)
		}
		return n
	}

	if _, err := database.Exec(
		`INSERT INTO functions (name, name_parts, file_path, start_line, end_line, language, exported)
		 VALUES (?1, split_identifier(?1), ?2, ?3, ?4, ?5, ?6)`,
		"ResolverCache", "a.go", 1, 5, "go", 1); err != nil {
		t.Fatal(err)
	}
	if got := matches(`"resolver" "cache"`); got != 1 {
		t.Fatalf("after insert: %d matches, want 1", got)
	}

	if _, err := database.Exec(
		`UPDATE functions SET name = ?1, name_parts = split_identifier(?1) WHERE name = 'ResolverCache'`,
		"PaymentProcessor"); err != nil {
		t.Fatal(err)
	}
	if got := matches(`"resolver" "cache"`); got != 0 {
		t.Errorf("after rename: %d stale matches for the old name, want 0", got)
	}
	if got := matches(`"payment" "processor"`); got != 1 {
		t.Errorf("after rename: %d matches for the new name, want 1", got)
	}

	if _, err := database.Exec(`DELETE FROM functions WHERE name = 'PaymentProcessor'`); err != nil {
		t.Fatal(err)
	}
	if got := matches(`"payment" "processor"`); got != 0 {
		t.Errorf("after delete: %d stale matches, want 0", got)
	}
}
