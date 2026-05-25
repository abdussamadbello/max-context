package indexer

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/maxcontext/max-context/internal/artifacts"
	"github.com/maxcontext/max-context/internal/db"
)

const batchSize = 500

// RunWorker consumes paths from ch and reindexes each file. Empty string means full reindex (Phase 4: .reindex-queue).
func RunWorker(ctx context.Context, root string, database *sql.DB, q *db.Queries, ch <-chan string) {
	for {
		select {
		case <-ctx.Done():
			return
		case path, ok := <-ch:
			if !ok {
				return
			}
			if path == "" {
				_ = Index(ctx, root, database, q)
				dir := filepath.Join(root, ".max-context")
				_ = artifacts.WriteSummary(dir, database)
				_ = artifacts.WriteArchitecture(dir, database)
				var totalFuncs, totalFiles int
				_ = database.QueryRow("SELECT COUNT(*) FROM functions").Scan(&totalFuncs)
				_ = database.QueryRow("SELECT COUNT(DISTINCT file_path) FROM functions").Scan(&totalFiles)
				_ = artifacts.WriteStatus(dir, &artifacts.Status{
					Healthy: true, LastFullIndex: time.Now(), TotalFunctions: totalFuncs, TotalFiles: totalFiles, Version: "0.1.0",
				})
				continue
			}
			_ = IndexFile(ctx, root, path, database, q)
		}
	}
}

// Index runs a full index of the project at root, using the given database and queries.
func Index(ctx context.Context, root string, database *sql.DB, q *db.Queries) error {
	ignore, _ := NewIgnoreMatcher(root)
	sc := &Scanner{Root: root, Ignore: ignore}
	files, err := sc.Scan()
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Clear existing data for full reindex
	if _, err := tx.Exec("DELETE FROM calls"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM functions"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM types"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM imports"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM file_summaries"); err != nil {
		return err
	}

	// Collect all results
	var allFuncs []FuncRecord
	var allCalls []CallRecord
	var allTypes []TypeRecord
	var allImports []ImportRecord

	for i, relPath := range files {
		if i%100 == 0 && i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
		fullPath := filepath.Join(root, relPath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}
		res, err := ParseFile(ctx, relPath, content)
		if err != nil {
			continue
		}
		allFuncs = append(allFuncs, res.Functions...)
		allCalls = append(allCalls, res.Calls...)
		allTypes = append(allTypes, res.Types...)
		allImports = append(allImports, res.Imports...)
	}

	// Insert functions and build name->id
	funcIDByKey := make(map[string]int64)
	for _, f := range allFuncs {
		var exported int
		if f.Exported {
			exported = 1
		}
		res, err := tx.Exec(
			"INSERT INTO functions (name, file_path, start_line, end_line, language, exported, code, docstring, signature) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			f.Name, f.FilePath, f.StartLine, f.EndLine, f.Language, exported, f.Code, f.Docstring, f.Signature,
		)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		key := f.FilePath + ":" + f.Name
		funcIDByKey[key] = id
	}

	// Insert calls with resolved callee_id and caller_id (functions already in tx).
	// Skip calls with no enclosing function (caller_id 0) to avoid FOREIGN KEY violation.
	for _, c := range allCalls {
		callerID, _ := CallerForLineTx(tx, c.FilePath, c.Line)
		if callerID == 0 {
			continue
		}
		calleeIDs := findFuncIDsByNameTx(tx, c.CalleeName)
		var calleeID interface{}
		if len(calleeIDs) > 0 {
			calleeID = calleeIDs[0]
		}
		_, err := tx.Exec(
			"INSERT INTO calls (caller_id, callee_id, callee_name, file_path, line) VALUES (?, ?, ?, ?, ?)",
			callerID, calleeID, c.CalleeName, c.FilePath, c.Line,
		)
		if err != nil {
			return err
		}
	}

	for _, t := range allTypes {
		var exported int
		if t.Exported {
			exported = 1
		}
		_, err := tx.Exec(
			"INSERT INTO types (name, file_path, kind, definition, exported) VALUES (?, ?, ?, ?, ?)",
			t.Name, t.FilePath, t.Kind, t.Definition, exported,
		)
		if err != nil {
			return err
		}
	}

	for _, imp := range allImports {
		_, err := tx.Exec(
			"INSERT INTO imports (file_path, imported_path, imported_symbols) VALUES (?, ?, ?)",
			imp.FilePath, imp.ImportedPath, imp.ImportedSymbols,
		)
		if err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return db.RebuildAllFTS(database)
}

func findFuncIDsByName(db *sql.DB, name string) []int64 {
	rows, err := db.Query("SELECT id FROM functions WHERE name = ?", name)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func findFuncIDsByNameTx(tx *sql.Tx, name string) []int64 {
	rows, err := tx.Query("SELECT id FROM functions WHERE name = ?", name)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// IndexFile reindexes a single file (incremental). Call after file change.
func IndexFile(ctx context.Context, root string, relPath string, database *sql.DB, q *db.Queries) error {
	fullPath := filepath.Join(root, relPath)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return err
	}

	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	path := filepath.ToSlash(relPath)
	for _, sql := range []string{
		"DELETE FROM functions WHERE file_path = ?",
		"DELETE FROM calls WHERE file_path = ?",
		"DELETE FROM types WHERE file_path = ?",
		"DELETE FROM imports WHERE file_path = ?",
		"DELETE FROM file_summaries WHERE file_path = ?",
	} {
		if _, err := tx.Exec(sql, path); err != nil {
			return err
		}
	}

	res, err := ParseFile(ctx, relPath, content)
	if err != nil {
		return err
	}

	for _, f := range res.Functions {
		var exported int
		if f.Exported {
			exported = 1
		}
		_, err := tx.Exec(
			"INSERT INTO functions (name, file_path, start_line, end_line, language, exported, code, docstring, signature) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			f.Name, f.FilePath, f.StartLine, f.EndLine, f.Language, exported, f.Code, f.Docstring, f.Signature,
		)
		if err != nil {
			return err
		}
	}

	for _, c := range res.Calls {
		callerID, _ := CallerForLineTx(tx, c.FilePath, c.Line)
		if callerID == 0 {
			continue
		}
		var calleeID interface{}
		for _, fid := range findFuncIDsByNameTx(tx, c.CalleeName) {
			calleeID = fid
			break
		}
		_, err := tx.Exec(
			"INSERT INTO calls (caller_id, callee_id, callee_name, file_path, line) VALUES (?, ?, ?, ?, ?)",
			callerID, calleeID, c.CalleeName, c.FilePath, c.Line,
		)
		if err != nil {
			return err
		}
	}

	for _, t := range res.Types {
		var exported int
		if t.Exported {
			exported = 1
		}
		_, err := tx.Exec(
			"INSERT INTO types (name, file_path, kind, definition, exported) VALUES (?, ?, ?, ?, ?)",
			t.Name, t.FilePath, t.Kind, t.Definition, exported,
		)
		if err != nil {
			return err
		}
	}

	for _, imp := range res.Imports {
		_, err := tx.Exec(
			"INSERT INTO imports (file_path, imported_path, imported_symbols) VALUES (?, ?, ?)",
			imp.FilePath, imp.ImportedPath, imp.ImportedSymbols,
		)
		if err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return db.RebuildAllFTS(database)
}
