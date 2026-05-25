package db

import (
	"database/sql"
	"fmt"
)

// Queries holds prepared statements for the lifetime of the process.
type Queries struct {
	SearchFunctions   *sql.Stmt
	SearchTypes       *sql.Stmt
	GetCallersOf      *sql.Stmt
	GetCalleesOf      *sql.Stmt
	InsertFunction    *sql.Stmt
	InsertCall        *sql.Stmt
	InsertType        *sql.Stmt
	InsertImport      *sql.Stmt
	InsertFileSummary *sql.Stmt
	InsertChange      *sql.Stmt
	DeleteFunctionsByFile *sql.Stmt
	DeleteCallsByFile     *sql.Stmt
	DeleteTypesByFile     *sql.Stmt
	DeleteImportsByFile   *sql.Stmt
	DeleteFileSummary     *sql.Stmt
	GetStats          *sql.Stmt
}

// PrepareQueries creates and caches all prepared statements. Call after Open and Migrate.
func PrepareQueries(db *sql.DB) (*Queries, error) {
	q := &Queries{}
	var err error

	q.SearchFunctions, err = db.Prepare(`
		SELECT f.id, f.name, f.file_path, f.start_line, f.end_line, f.language, f.exported, f.code, f.docstring, f.signature,
		       snippet(functions_fts, 2, '»', '«', '...', 30) AS snippet,
		       bm25(functions_fts, 10.0, 5.0, 1.0, 2.0) AS rank
		FROM functions_fts
		JOIN functions f ON f.id = functions_fts.rowid
		WHERE functions_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare SearchFunctions: %w", err)
	}

	q.SearchTypes, err = db.Prepare(`
		SELECT t.id, t.name, t.file_path, t.kind, t.definition, t.exported,
		       snippet(types_fts, 2, '»', '«', '...', 30) AS snippet,
		       bm25(types_fts, 10.0, 5.0, 1.0) AS rank
		FROM types_fts
		JOIN types t ON t.id = types_fts.rowid
		WHERE types_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare SearchTypes: %w", err)
	}

	q.GetCallersOf, err = db.Prepare(`
		SELECT f.name FROM calls c JOIN functions f ON f.id = c.caller_id WHERE c.callee_id = ?
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare GetCallersOf: %w", err)
	}

	q.GetCalleesOf, err = db.Prepare(`
		SELECT COALESCE(f.name, c.callee_name) FROM calls c LEFT JOIN functions f ON f.id = c.callee_id WHERE c.caller_id = ?
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare GetCalleesOf: %w", err)
	}

	q.InsertFunction, err = db.Prepare(`
		INSERT INTO functions (name, file_path, start_line, end_line, language, exported, code, docstring, signature)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare InsertFunction: %w", err)
	}

	q.InsertCall, err = db.Prepare(`
		INSERT INTO calls (caller_id, callee_id, callee_name, file_path, line) VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare InsertCall: %w", err)
	}

	q.InsertType, err = db.Prepare(`
		INSERT INTO types (name, file_path, kind, definition, exported) VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare InsertType: %w", err)
	}

	q.InsertImport, err = db.Prepare(`
		INSERT INTO imports (file_path, imported_path, imported_symbols) VALUES (?, ?, ?)
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare InsertImport: %w", err)
	}

	q.InsertFileSummary, err = db.Prepare(`
		INSERT OR REPLACE INTO file_summaries (file_path, language, function_count, type_count, import_count, top_functions, detected_patterns)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare InsertFileSummary: %w", err)
	}

	q.InsertChange, err = db.Prepare(`
		INSERT INTO changes (file_path, change_type, timestamp, session_id) VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare InsertChange: %w", err)
	}

	q.DeleteFunctionsByFile, err = db.Prepare(`DELETE FROM functions WHERE file_path = ?`)
	if err != nil {
		return nil, fmt.Errorf("prepare DeleteFunctionsByFile: %w", err)
	}

	q.DeleteCallsByFile, err = db.Prepare(`DELETE FROM calls WHERE file_path = ?`)
	if err != nil {
		return nil, fmt.Errorf("prepare DeleteCallsByFile: %w", err)
	}

	q.DeleteTypesByFile, err = db.Prepare(`DELETE FROM types WHERE file_path = ?`)
	if err != nil {
		return nil, fmt.Errorf("prepare DeleteTypesByFile: %w", err)
	}

	q.DeleteImportsByFile, err = db.Prepare(`DELETE FROM imports WHERE file_path = ?`)
	if err != nil {
		return nil, fmt.Errorf("prepare DeleteImportsByFile: %w", err)
	}

	q.DeleteFileSummary, err = db.Prepare(`DELETE FROM file_summaries WHERE file_path = ?`)
	if err != nil {
		return nil, fmt.Errorf("prepare DeleteFileSummary: %w", err)
	}

	q.GetStats, err = db.Prepare(`
		SELECT
			(SELECT COUNT(*) FROM functions),
			(SELECT COUNT(DISTINCT file_path) FROM functions),
			(SELECT COUNT(*) FROM types)
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare GetStats: %w", err)
	}

	return q, nil
}

// Close closes all prepared statements.
func (q *Queries) Close() error {
	for _, stmt := range []*sql.Stmt{
		q.SearchFunctions, q.SearchTypes, q.GetCallersOf, q.GetCalleesOf,
		q.InsertFunction, q.InsertCall, q.InsertType, q.InsertImport,
		q.InsertFileSummary, q.InsertChange,
		q.DeleteFunctionsByFile, q.DeleteCallsByFile, q.DeleteTypesByFile, q.DeleteImportsByFile, q.DeleteFileSummary,
		q.GetStats,
	} {
		if stmt != nil {
			_ = stmt.Close()
		}
	}
	return nil
}
