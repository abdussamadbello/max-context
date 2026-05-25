package db

import (
	"database/sql"
	"fmt"
	"strings"
)

const schemaVersion = 1

// Migrate ensures the database schema is at the current version, running
// migrations if needed. Call after Open.
func Migrate(db *sql.DB) error {
	v, err := getSchemaVersion(db)
	if err != nil {
		return err
	}
	if v >= schemaVersion {
		return nil
	}
	for i := v + 1; i <= schemaVersion; i++ {
		if err := runMigration(db, i); err != nil {
			return fmt.Errorf("migration %d: %w", i, err)
		}
	}
	return nil
}

func getSchemaVersion(db *sql.DB) (int, error) {
	var value sql.NullString
	err := db.QueryRow("SELECT value FROM _meta WHERE key = ?", "schema_version").Scan(&value)
	if err == sql.ErrNoRows || (err == nil && !value.Valid) {
		return 0, nil
	}
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return 0, nil
		}
		return 0, err
	}
	var v int
	if _, err := fmt.Sscanf(value.String, "%d", &v); err != nil {
		return 0, fmt.Errorf("parse schema_version: %w", err)
	}
	return v, nil
}

func setSchemaVersion(tx *sql.Tx, version int) error {
	_, err := tx.Exec("INSERT OR REPLACE INTO _meta (key, value) VALUES (?, ?)", "schema_version", fmt.Sprintf("%d", version))
	return err
}

func runMigration(db *sql.DB, version int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	fn, ok := migrations[version]
	if !ok {
		return fmt.Errorf("unknown migration version %d", version)
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := setSchemaVersion(tx, version); err != nil {
		return err
	}
	return tx.Commit()
}

var migrations = map[int]func(*sql.Tx) error{
	1: migrationV1,
}

func migrationV1(tx *sql.Tx) error {
	// _meta for schema version and key-value store
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS _meta (
			key TEXT PRIMARY KEY,
			value TEXT
		);
	`)
	if err != nil {
		return err
	}

	// functions: indexed symbols (functions, methods)
	_, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS functions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			file_path TEXT NOT NULL,
			start_line INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			language TEXT NOT NULL,
			exported INTEGER NOT NULL DEFAULT 0,
			code TEXT,
			docstring TEXT,
			signature TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_functions_file ON functions(file_path);
		CREATE INDEX IF NOT EXISTS idx_functions_name ON functions(name);
	`)
	if err != nil {
		return err
	}

	// calls: caller -> callee edges (callee_id NULL for external)
	_, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS calls (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			caller_id INTEGER NOT NULL REFERENCES functions(id),
			callee_id INTEGER REFERENCES functions(id),
			callee_name TEXT NOT NULL,
			file_path TEXT NOT NULL,
			line INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_calls_caller ON calls(caller_id);
		CREATE INDEX IF NOT EXISTS idx_calls_callee ON calls(callee_id);
		CREATE INDEX IF NOT EXISTS idx_calls_file ON calls(file_path);
	`)
	if err != nil {
		return err
	}

	// types: interfaces, type aliases, structs
	_, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS types (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			file_path TEXT NOT NULL,
			kind TEXT NOT NULL,
			definition TEXT,
			exported INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_types_file ON types(file_path);
		CREATE INDEX IF NOT EXISTS idx_types_name ON types(name);
	`)
	if err != nil {
		return err
	}

	// imports: per-file import relationships
	_, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS imports (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			file_path TEXT NOT NULL,
			imported_path TEXT NOT NULL,
			imported_symbols TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_imports_file ON imports(file_path);
		CREATE INDEX IF NOT EXISTS idx_imports_imported ON imports(imported_path);
	`)
	if err != nil {
		return err
	}

	// file_summaries: per-file aggregates
	_, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS file_summaries (
			file_path TEXT PRIMARY KEY,
			language TEXT NOT NULL,
			function_count INTEGER NOT NULL DEFAULT 0,
			type_count INTEGER NOT NULL DEFAULT 0,
			import_count INTEGER NOT NULL DEFAULT 0,
			top_functions TEXT,
			detected_patterns TEXT
		);
	`)
	if err != nil {
		return err
	}

	// changes: audit log for incremental reindex
	_, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS changes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			file_path TEXT NOT NULL,
			change_type TEXT NOT NULL,
			timestamp INTEGER NOT NULL,
			session_id TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_changes_file ON changes(file_path);
	`)
	if err != nil {
		return err
	}

	// FTS5 external content tables: content lives in functions/types; rebuild with INSERT INTO x_fts(x_fts) VALUES('rebuild')
	_, err = tx.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS functions_fts USING fts5(
			name,
			file_path,
			code,
			docstring,
			content='functions',
			content_rowid='id'
		);
	`)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS types_fts USING fts5(
			name,
			file_path,
			definition,
			content='types',
			content_rowid='id'
		);
	`)
	if err != nil {
		return err
	}

	return nil
}
