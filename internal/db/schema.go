package db

import (
	"database/sql"
	"fmt"
	"strings"
)

const schemaVersion = 11

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
	1:  migrationV1,
	2:  migrationV2,
	3:  migrationV3,
	4:  migrationV4,
	5:  migrationV5,
	6:  migrationV6,
	7:  migrationV7,
	8:  migrationV8,
	9:  migrationV9,
	10: migrationV10,
	11: migrationV11,
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

// migrationV2 adds call-edge resolution confidence and the definition-side
// metadata (kind, receiver type, package) needed by the scope resolver.
//
// Backward compatible: calls.resolution defaults to 'name-global' (the prior
// semantics), and the new functions columns default safely, so rows written by
// v1 and by languages without a scope resolver read back unchanged. A full
// reindex is recommended after upgrade so the new columns populate and edges
// pick up precise resolution.
func migrationV2(tx *sql.Tx) error {
	// calls: per-edge resolution confidence marker + the call-site receiver.
	// resolution vocabulary (high->low confidence): same-file, same-package,
	// receiver-typed, import-qualified, name-global; builtin/unresolved = no link.
	_, err := tx.Exec(`
		ALTER TABLE calls ADD COLUMN resolution TEXT NOT NULL DEFAULT 'name-global';
		ALTER TABLE calls ADD COLUMN receiver_name TEXT;
		CREATE INDEX IF NOT EXISTS idx_calls_resolution ON calls(resolution);
	`)
	if err != nil {
		return err
	}

	// functions: definition-side metadata for resolution.
	//   kind: 'func' | 'method'
	//   receiver_type: e.g. 'Flags' for func (f *Flags) X(); NULL for plain funcs
	//   package: the file's package name, for same-package lookups
	_, err = tx.Exec(`
		ALTER TABLE functions ADD COLUMN kind TEXT NOT NULL DEFAULT 'func';
		ALTER TABLE functions ADD COLUMN receiver_type TEXT;
		ALTER TABLE functions ADD COLUMN package TEXT;
		CREATE INDEX IF NOT EXISTS idx_functions_pkg_name ON functions(package, name);
		CREATE INDEX IF NOT EXISTS idx_functions_recv ON functions(receiver_type, name);
	`)
	if err != nil {
		return err
	}

	return nil
}

// migrationV3 (Phase 9a) records each function's single-value return type, so
// the resolver can type locals bound by `x := someCall()` and resolve method
// calls on them. NULL for multi-return, tuple, interface, or otherwise
// non-receiver-usable returns.
func migrationV3(tx *sql.Tx) error {
	_, err := tx.Exec(`ALTER TABLE functions ADD COLUMN return_type TEXT;`)
	return err
}

// migrationV4 (Phase 9b) adds struct-field and package-level var type maps so
// the resolver can type field receivers (s.db.Query()) and package-global
// receivers (globalCfg.Method()). Both are pure metadata tables, repopulated on
// reindex; defaults keep non-Go and pre-existing data unaffected.
func migrationV4(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS struct_fields (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			struct_type TEXT NOT NULL,
			field_name  TEXT NOT NULL,
			field_type  TEXT NOT NULL,
			package     TEXT,
			file_path   TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_struct_fields_lookup ON struct_fields(struct_type, field_name);
		CREATE INDEX IF NOT EXISTS idx_struct_fields_file ON struct_fields(file_path);
	`)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS package_vars (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name      TEXT NOT NULL,
			var_type  TEXT NOT NULL,
			package   TEXT,
			file_path TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_package_vars_lookup ON package_vars(package, name);
		CREATE INDEX IF NOT EXISTS idx_package_vars_file ON package_vars(file_path);
	`)
	return err
}

// migrationV5 adds class inheritance edges so the resolver can resolve calls to
// INHERITED methods — e.g. Client(BaseClient) calling self.build_request() where
// build_request is defined on BaseClient. Without this, self.<inherited>() calls
// go unresolved, blinding the call graph on real OOP codebases (the dominant
// Python/TS pattern). Repopulated on reindex; empty for languages/files w/o classes.
func migrationV5(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS class_bases (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			class_name TEXT NOT NULL,
			base_name  TEXT NOT NULL,
			package    TEXT,
			file_path  TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_class_bases_lookup ON class_bases(class_name);
		CREATE INDEX IF NOT EXISTS idx_class_bases_file ON class_bases(file_path);
	`)
	return err
}

// migrationV6 makes the FTS5 indexes self-maintaining via sync triggers, instead
// of a post-commit full `'rebuild'`. The old flow committed the data transaction,
// then rebuilt the entire external-content FTS as a separate step — leaving a
// window where a concurrent query_codebase saw an empty/locked FTS table (the
// background reindex worker racing reader queries; this was the true cause of the
// E01 over-exploration). With AFTER INSERT/UPDATE/DELETE triggers, every data
// mutation updates the FTS index INSIDE the same transaction, so in WAL mode a
// reader sees the old committed state until a single atomic flip to the new one —
// no empty window — and incremental edits cost O(changed rows), not O(repo).
//
// For an FTS5 external-content table, deletes/updates must push the OLD row values
// through the special 'delete' command to keep the index consistent; inserts/the
// new side of an update push the new values.
func migrationV6(tx *sql.Tx) error {
	_, err := tx.Exec(`
		-- functions_fts (content='functions', columns: name, file_path, code, docstring)
		CREATE TRIGGER IF NOT EXISTS functions_ai AFTER INSERT ON functions BEGIN
			INSERT INTO functions_fts(rowid, name, file_path, code, docstring)
			VALUES (new.id, new.name, new.file_path, new.code, new.docstring);
		END;
		CREATE TRIGGER IF NOT EXISTS functions_ad AFTER DELETE ON functions BEGIN
			INSERT INTO functions_fts(functions_fts, rowid, name, file_path, code, docstring)
			VALUES ('delete', old.id, old.name, old.file_path, old.code, old.docstring);
		END;
		CREATE TRIGGER IF NOT EXISTS functions_au AFTER UPDATE ON functions BEGIN
			INSERT INTO functions_fts(functions_fts, rowid, name, file_path, code, docstring)
			VALUES ('delete', old.id, old.name, old.file_path, old.code, old.docstring);
			INSERT INTO functions_fts(rowid, name, file_path, code, docstring)
			VALUES (new.id, new.name, new.file_path, new.code, new.docstring);
		END;

		-- types_fts (content='types', columns: name, file_path, definition)
		CREATE TRIGGER IF NOT EXISTS types_ai AFTER INSERT ON types BEGIN
			INSERT INTO types_fts(rowid, name, file_path, definition)
			VALUES (new.id, new.name, new.file_path, new.definition);
		END;
		CREATE TRIGGER IF NOT EXISTS types_ad AFTER DELETE ON types BEGIN
			INSERT INTO types_fts(types_fts, rowid, name, file_path, definition)
			VALUES ('delete', old.id, old.name, old.file_path, old.definition);
		END;
		CREATE TRIGGER IF NOT EXISTS types_au AFTER UPDATE ON types BEGIN
			INSERT INTO types_fts(types_fts, rowid, name, file_path, definition)
			VALUES ('delete', old.id, old.name, old.file_path, old.definition);
			INSERT INTO types_fts(rowid, name, file_path, definition)
			VALUES (new.id, new.name, new.file_path, new.definition);
		END;
	`)
	if err != nil {
		return err
	}
	// Bring the FTS content current with whatever rows already exist (one final
	// full rebuild at migration time; triggers maintain it incrementally after).
	if _, err := tx.Exec("INSERT INTO functions_fts(functions_fts) VALUES('rebuild')"); err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO types_fts(types_fts) VALUES('rebuild')")
	return err
}

// migrationV7 adds index_errors, recording per-file indexing failures so they
// surface as staleness in tool responses and --status instead of being silently
// swallowed by the background worker (which previously did `_ = IndexFile(...)`).
// file_path is the PRIMARY KEY so a retry replaces the prior failure and a clean
// reindex deletes the row; the sentinel '<full-index>' records a whole-repo
// index failure. Pure additive metadata; empty on a healthy index.
func migrationV7(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS index_errors (
			file_path TEXT PRIMARY KEY,
			error     TEXT NOT NULL,
			timestamp INTEGER NOT NULL
		);
	`)
	return err
}

// migrationV8 adds plain-text document indexing: non-code files (markdown,
// YAML, JSON, TOML, proto, GraphQL, SQL, XML, Dockerfiles) stored whole in
// `documents` and searched via an external-content FTS5 table, so
// query_codebase can answer schema/config/docs questions the code index is
// blind to. Same self-maintaining trigger pattern as migrationV6: the
// 'delete' command form keeps the external-content index consistent on
// delete/update, and every mutation updates FTS inside the same transaction.
func migrationV8(tx *sql.Tx) error {
	_, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS documents (
			id        INTEGER PRIMARY KEY AUTOINCREMENT,
			file_path TEXT NOT NULL,
			title     TEXT,
			kind      TEXT NOT NULL,
			content   TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_documents_file ON documents(file_path);

		CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
			title,
			file_path,
			content,
			content='documents',
			content_rowid='id'
		);

		CREATE TRIGGER IF NOT EXISTS documents_ai AFTER INSERT ON documents BEGIN
			INSERT INTO documents_fts(rowid, title, file_path, content)
			VALUES (new.id, new.title, new.file_path, new.content);
		END;
		CREATE TRIGGER IF NOT EXISTS documents_ad AFTER DELETE ON documents BEGIN
			INSERT INTO documents_fts(documents_fts, rowid, title, file_path, content)
			VALUES ('delete', old.id, old.title, old.file_path, old.content);
		END;
		CREATE TRIGGER IF NOT EXISTS documents_au AFTER UPDATE ON documents BEGIN
			INSERT INTO documents_fts(documents_fts, rowid, title, file_path, content)
			VALUES ('delete', old.id, old.title, old.file_path, old.content);
			INSERT INTO documents_fts(rowid, title, file_path, content)
			VALUES (new.id, new.title, new.file_path, new.content);
		END;
	`)
	return err
}

// migrationV9 makes multi-word queries reach camelCase and snake_case symbols,
// and records where a type is defined.
//
// FTS5's tokenizer indexes "ResolverCache" as one token, so the query
// "resolver cache" matched nothing in the name column — but matched the
// indexed file_path of every symbol in resolver_cache_test.go, which then
// outranked the type itself. Agents ask in words, so a name_parts column
// carries the split form ("Resolver Cache") alongside the raw name, and
// file_path is down-weighted in the search queries so a path match can no
// longer outrank a name match.
//
// types also gains start_line: the table never stored one, so every type
// result came back with a file but no line to jump to.
//
// The FTS tables are dropped and recreated (they are derived data, rebuilt
// from the content tables in the same transaction), and name_parts is
// backfilled for existing rows so an upgrade does not require a reindex.
func migrationV9(tx *sql.Tx) error {
	// Additive columns on the content tables.
	for _, stmt := range []string{
		`ALTER TABLE functions ADD COLUMN name_parts TEXT`,
		`ALTER TABLE types ADD COLUMN name_parts TEXT`,
		`ALTER TABLE types ADD COLUMN start_line INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}

	// Backfill the split form for rows already indexed, so upgrading does not
	// require a reindex to get the new matching behaviour.
	for _, table := range []string{"functions", "types"} {
		if _, err := tx.Exec("UPDATE " + table + " SET name_parts = split_identifier(name)"); err != nil {
			return fmt.Errorf("backfill %s.name_parts: %w", table, err)
		}
	}

	// Recreate the FTS tables and their triggers with the new column. Dropping
	// the triggers first avoids them firing against the old shape mid-migration.
	_, err := tx.Exec(`
		DROP TRIGGER IF EXISTS functions_ai;
		DROP TRIGGER IF EXISTS functions_ad;
		DROP TRIGGER IF EXISTS functions_au;
		DROP TRIGGER IF EXISTS types_ai;
		DROP TRIGGER IF EXISTS types_ad;
		DROP TRIGGER IF EXISTS types_au;
		DROP TABLE IF EXISTS functions_fts;
		DROP TABLE IF EXISTS types_fts;

		CREATE VIRTUAL TABLE functions_fts USING fts5(
			name,
			name_parts,
			file_path,
			code,
			docstring,
			content='functions',
			content_rowid='id'
		);
		CREATE VIRTUAL TABLE types_fts USING fts5(
			name,
			name_parts,
			file_path,
			definition,
			content='types',
			content_rowid='id'
		);

		CREATE TRIGGER functions_ai AFTER INSERT ON functions BEGIN
			INSERT INTO functions_fts(rowid, name, name_parts, file_path, code, docstring)
			VALUES (new.id, new.name, new.name_parts, new.file_path, new.code, new.docstring);
		END;
		CREATE TRIGGER functions_ad AFTER DELETE ON functions BEGIN
			INSERT INTO functions_fts(functions_fts, rowid, name, name_parts, file_path, code, docstring)
			VALUES ('delete', old.id, old.name, old.name_parts, old.file_path, old.code, old.docstring);
		END;
		CREATE TRIGGER functions_au AFTER UPDATE ON functions BEGIN
			INSERT INTO functions_fts(functions_fts, rowid, name, name_parts, file_path, code, docstring)
			VALUES ('delete', old.id, old.name, old.name_parts, old.file_path, old.code, old.docstring);
			INSERT INTO functions_fts(rowid, name, name_parts, file_path, code, docstring)
			VALUES (new.id, new.name, new.name_parts, new.file_path, new.code, new.docstring);
		END;

		CREATE TRIGGER types_ai AFTER INSERT ON types BEGIN
			INSERT INTO types_fts(rowid, name, name_parts, file_path, definition)
			VALUES (new.id, new.name, new.name_parts, new.file_path, new.definition);
		END;
		CREATE TRIGGER types_ad AFTER DELETE ON types BEGIN
			INSERT INTO types_fts(types_fts, rowid, name, name_parts, file_path, definition)
			VALUES ('delete', old.id, old.name, old.name_parts, old.file_path, old.definition);
		END;
		CREATE TRIGGER types_au AFTER UPDATE ON types BEGIN
			INSERT INTO types_fts(types_fts, rowid, name, name_parts, file_path, definition)
			VALUES ('delete', old.id, old.name, old.name_parts, old.file_path, old.definition);
			INSERT INTO types_fts(rowid, name, name_parts, file_path, definition)
			VALUES (new.id, new.name, new.name_parts, new.file_path, new.definition);
		END;
	`)
	if err != nil {
		return err
	}

	if _, err := tx.Exec("INSERT INTO functions_fts(functions_fts) VALUES('rebuild')"); err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO types_fts(types_fts) VALUES('rebuild')")
	return err
}

// migrationV10 records how many concrete implementations each interface-dispatch
// call site fans out to, so the default confidence filter can distinguish an
// unambiguous dispatch from a noisy one.
//
// Measured on three real Go repositories (cobra, gin, client_golang), dispatch
// edges are 0–4% of the call graph, and their fan-out width is bimodal: 55% of
// call sites resolve to two implementations or fewer, while a fifth resolve to
// 13 or 19. Including every dispatch edge grew responses by a median of 5–87%
// but by 872% and 1138% at the tail, and the blowups were exactly the wide
// sites. Width is what separates the two, so it is recorded per edge rather
// than recomputed per query inside a recursive walk.
//
// 0 means "not an interface-dispatch edge". Rows indexed before this migration
// keep 0 and are treated as wide (excluded by default) until reindexed, which
// preserves the previous behaviour rather than silently widening an old index.
func migrationV10(tx *sql.Tx) error {
	for _, stmt := range []string{
		`ALTER TABLE calls ADD COLUMN dispatch_width INTEGER NOT NULL DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS idx_calls_dispatch_width ON calls(dispatch_width)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	return nil
}

// migrationV11 adds the SCIP-shaped symbol string for each definition.
//
// Every tool addresses code by bare name, which cannot express the question a
// caller usually means: EmailNotifier.Send, SMSNotifier.Send and an unrelated
// MetricsBuffer.Send all collapse into "Send". The symbol carries the package
// and receiver type alongside the name, so the three become distinguishable
// without changing how the fuzzy lookups work.
//
// Empty until reindexed. A definition with no symbol is simply not addressable
// that way, which degrades to today's behaviour rather than matching wrongly.
func migrationV11(tx *sql.Tx) error {
	for _, stmt := range []string{
		`ALTER TABLE functions ADD COLUMN symbol TEXT`,
		`CREATE INDEX IF NOT EXISTS idx_functions_symbol ON functions(symbol)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	return nil
}
