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
	var allStructFields []StructFieldRecord
	var allPackageVars []PackageVarRecord
	var allClassBases []ClassBaseRecord

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
		allStructFields = append(allStructFields, res.StructFields...)
		allPackageVars = append(allPackageVars, res.PackageVars...)
		allClassBases = append(allClassBases, res.ClassBases...)
		// Module-level constants become searchable symbols in the types table
		// (kind=constant), so query_codebase + get_definition find them.
		for _, c := range res.Consts {
			allTypes = append(allTypes, constToType(c))
		}
	}

	// Insert functions (with resolution metadata) and remember each file's package.
	fileToPkg := make(map[string]string)
	for _, f := range allFuncs {
		if err := insertFunction(tx, f); err != nil {
			return err
		}
		if f.Package != "" {
			fileToPkg[f.FilePath] = f.Package
		}
	}

	// Insert the field/global type maps (9b) before building the resolver, which
	// reads them for field- and global-receiver typing.
	for _, sf := range allStructFields {
		if err := insertStructField(tx, sf); err != nil {
			return err
		}
	}
	for _, pv := range allPackageVars {
		if err := insertPackageVar(tx, pv); err != nil {
			return err
		}
	}
	for _, cb := range allClassBases {
		if err := insertClassBase(tx, cb); err != nil {
			return err
		}
	}

	// Insert types before building the resolver so it can recognize class/struct
	// names (for constructor-typed locals like `x = Conn()`).
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

	// Resolve callee ids using scope rules. The resolver reads the functions,
	// type maps, and class names just inserted (visible within this transaction).
	resolver, err := NewResolver(tx)
	if err != nil {
		return err
	}

	// Insert calls with resolved callee_id and caller_id.
	// Skip calls with no enclosing function (caller_id 0) to avoid FOREIGN KEY violation.
	for _, c := range allCalls {
		callerID, _ := CallerForLineTx(tx, c.FilePath, c.Line)
		if callerID == 0 {
			continue
		}
		if err := insertCall(tx, resolver, c, callerID, fileToPkg[c.FilePath]); err != nil {
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

	// FTS is maintained by sync triggers (schema V6) inside this transaction, so
	// the commit flips data + FTS atomically — no separate post-commit rebuild and
	// no window where a concurrent query sees an empty FTS index.
	return tx.Commit()
}


// insertFunction writes one function row including the resolution metadata
// (kind, receiver_type, package) used by the scope resolver.
func insertFunction(tx *sql.Tx, f FuncRecord) error {
	var exported int
	if f.Exported {
		exported = 1
	}
	kind := f.Kind
	if kind == "" {
		kind = "func"
	}
	_, err := tx.Exec(
		"INSERT INTO functions (name, file_path, start_line, end_line, language, exported, code, docstring, signature, kind, receiver_type, package, return_type) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		f.Name, f.FilePath, f.StartLine, f.EndLine, f.Language, exported, f.Code, f.Docstring, f.Signature,
		kind, nullStr(f.ReceiverType), nullStr(f.Package), nullStr(f.ReturnType),
	)
	return err
}

// insertStructField writes one struct-field type mapping (9b).
func insertStructField(tx *sql.Tx, sf StructFieldRecord) error {
	_, err := tx.Exec(
		"INSERT INTO struct_fields (struct_type, field_name, field_type, package, file_path) VALUES (?, ?, ?, ?, ?)",
		sf.StructType, sf.FieldName, sf.FieldType, nullStr(sf.Package), sf.FilePath,
	)
	return err
}

// insertPackageVar writes one package-level var type mapping (9b).
func insertPackageVar(tx *sql.Tx, pv PackageVarRecord) error {
	_, err := tx.Exec(
		"INSERT INTO package_vars (name, var_type, package, file_path) VALUES (?, ?, ?, ?)",
		pv.Name, pv.VarType, nullStr(pv.Package), pv.FilePath,
	)
	return err
}

// constToType turns a module-level constant into a searchable type-table symbol
// (kind=constant), so query_codebase (FTS over types) and get_definition find it.
func constToType(c ConstRecord) TypeRecord {
	return TypeRecord{
		Name:       c.Name,
		FilePath:   c.FilePath,
		Kind:       "constant",
		Definition: c.Name,
		Exported:   isExported(c.Name),
	}
}

// insertClassBase writes one class→base inheritance edge (for inherited-method
// resolution).
func insertClassBase(tx *sql.Tx, cb ClassBaseRecord) error {
	_, err := tx.Exec(
		"INSERT INTO class_bases (class_name, base_name, package, file_path) VALUES (?, ?, ?, ?)",
		cb.ClassName, cb.BaseName, nullStr(cb.Package), cb.FilePath,
	)
	return err
}

// insertCall resolves the callee via scope rules and writes the edge with its
// resolution confidence marker. callerID is the (already-resolved, non-zero)
// enclosing function; callerPkg is the package of the call site's file.
func insertCall(tx *sql.Tx, resolver *Resolver, c CallRecord, callerID int64, callerPkg string) error {
	calleeID, marker := resolver.ResolveCall(c, callerPkg, c.FilePath)
	var calleeArg interface{}
	if calleeID != 0 {
		calleeArg = calleeID
	}
	_, err := tx.Exec(
		"INSERT INTO calls (caller_id, callee_id, callee_name, file_path, line, resolution, receiver_name) VALUES (?, ?, ?, ?, ?, ?, ?)",
		callerID, calleeArg, c.CalleeName, c.FilePath, c.Line, marker, nullStr(c.ReceiverName),
	)
	return err
}

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// IndexFile reindexes a single file (incremental). Call after file change.
func IndexFile(ctx context.Context, root string, relPath string, database *sql.DB, q *db.Queries) error {
	fullPath := filepath.Join(root, relPath)
	content, readErr := os.ReadFile(fullPath)
	deleted := false
	if readErr != nil {
		if !os.IsNotExist(readErr) {
			return readErr
		}
		// File removed on disk: treat as a deletion — drop its rows (and clear the
		// cross-file edges into it) instead of erroring, which previously left the
		// deleted file's symbols and dangling edges in the index forever.
		deleted = true
	}

	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	path := filepath.ToSlash(relPath)

	// Cross-file edges from OTHER files may reference this file's functions via
	// calls.callee_id. With foreign_keys=ON, deleting those functions while the
	// edges still point at them violates the FK and rolls back the whole reindex
	// (silently, since RunWorker discarded the error) — so edits to widely-called
	// files never reached the index. Null those references and mark them 'stale';
	// reresolveStaleEdges re-resolves them below, once this file's new functions
	// are in place (or leaves them unresolved if the target is gone). Must run
	// before the DELETE FROM functions so the subquery still sees the old rows.
	if _, err := tx.Exec(
		"UPDATE calls SET callee_id = NULL, resolution = ? WHERE callee_id IN (SELECT id FROM functions WHERE file_path = ?)",
		resStale, path,
	); err != nil {
		return err
	}

	// Delete calls before functions: calls.caller_id/callee_id reference
	// functions(id), so with foreign_keys=ON the edges must go first.
	for _, sql := range []string{
		"DELETE FROM calls WHERE file_path = ?",
		"DELETE FROM functions WHERE file_path = ?",
		"DELETE FROM types WHERE file_path = ?",
		"DELETE FROM imports WHERE file_path = ?",
		"DELETE FROM file_summaries WHERE file_path = ?",
		"DELETE FROM struct_fields WHERE file_path = ?",
		"DELETE FROM package_vars WHERE file_path = ?",
		"DELETE FROM class_bases WHERE file_path = ?",
	} {
		if _, err := tx.Exec(sql, path); err != nil {
			return err
		}
	}

	// A deleted file has nothing to parse or reinsert. Build the resolver against
	// the now-current tables, re-resolve the edges that pointed into it, commit.
	if deleted {
		resolver, err := NewResolver(tx)
		if err != nil {
			return err
		}
		if err := reresolveStaleEdges(ctx, tx, root, resolver); err != nil {
			return err
		}
		return tx.Commit()
	}

	res, err := ParseFile(ctx, relPath, content)
	if err != nil {
		return err
	}

	filePkg := ""
	for _, f := range res.Functions {
		if err := insertFunction(tx, f); err != nil {
			return err
		}
		if f.Package != "" {
			filePkg = f.Package
		}
	}
	for _, sf := range res.StructFields {
		if err := insertStructField(tx, sf); err != nil {
			return err
		}
	}
	for _, pv := range res.PackageVars {
		if err := insertPackageVar(tx, pv); err != nil {
			return err
		}
	}
	for _, cb := range res.ClassBases {
		if err := insertClassBase(tx, cb); err != nil {
			return err
		}
	}

	// Insert types before building the resolver (constructor-typed locals need
	// to recognize class names). Note: incremental indexing only sees this
	// file's types, so cross-file constructor typing falls back to unresolved
	// until the next full reindex — acceptable, never wrong.
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
	// Module-level constants -> searchable type-table symbols (kind=constant).
	for _, c := range res.Consts {
		ct := constToType(c)
		var exported int
		if ct.Exported {
			exported = 1
		}
		if _, err := tx.Exec(
			"INSERT INTO types (name, file_path, kind, definition, exported) VALUES (?, ?, ?, ?, ?)",
			ct.Name, ct.FilePath, ct.Kind, ct.Definition, exported,
		); err != nil {
			return err
		}
	}

	// Build the resolver from the full functions table (this file's rows were
	// just reinserted in the same tx), so same-package and receiver-typed
	// lookups see definitions in other files too.
	resolver, err := NewResolver(tx)
	if err != nil {
		return err
	}

	for _, c := range res.Calls {
		callerID, _ := CallerForLineTx(tx, c.FilePath, c.Line)
		if callerID == 0 {
			continue
		}
		if err := insertCall(tx, resolver, c, callerID, filePkg); err != nil {
			return err
		}
	}

	// Re-resolve edges from other files that pointed into this file (nulled +
	// marked 'stale' above) now that this file's new functions exist.
	if err := reresolveStaleEdges(ctx, tx, root, resolver); err != nil {
		return err
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

	// FTS is maintained by sync triggers (schema V6) inside this transaction, so
	// the commit flips data + FTS atomically — no separate post-commit rebuild and
	// no window where a concurrent query sees an empty FTS index.
	return tx.Commit()
}

// reresolveStaleEdges re-resolves call edges marked 'stale' — their callee lived
// in a file just reindexed (or deleted), so their callee_id was nulled to avoid
// an FK violation. For each distinct caller file holding stale edges it re-parses
// the file (the file itself was not edited, so its stored line numbers still
// match) to recover the call-site receiver context the calls table doesn't
// store, resolves each call against the rebuilt resolver, and writes the new
// callee_id + marker back. Edges whose target was renamed or deleted become
// unresolved — never a dangling id and never a stale confidence claim.
func reresolveStaleEdges(ctx context.Context, tx *sql.Tx, root string, resolver *Resolver) error {
	rows, err := tx.Query("SELECT DISTINCT file_path FROM calls WHERE resolution = ?", resStale)
	if err != nil {
		return err
	}
	var files []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			rows.Close()
			return err
		}
		files = append(files, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, f := range files {
		if err := reresolveStaleEdgesInFile(ctx, tx, root, resolver, f); err != nil {
			return err
		}
	}
	return nil
}

// staleResolved is a re-resolution result for one call site (callee id 0 = no link).
type staleResolved struct {
	id     int64
	marker string
}

// reresolveStaleEdgesInFile re-resolves the stale edges originating in caller
// file f. It parses f once, resolves every call against the current resolver,
// keys results by (line, callee_name), then updates each stale row in f. A file
// that no longer parses (or whose call site vanished) leaves its stale edges
// unresolved.
func reresolveStaleEdgesInFile(ctx context.Context, tx *sql.Tx, root string, resolver *Resolver, f string) error {
	parsed := map[string]staleResolved{}
	if content, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(f))); readErr == nil {
		if res, perr := ParseFile(ctx, f, content); perr == nil {
			pkg := ""
			for _, fn := range res.Functions {
				if fn.Package != "" {
					pkg = fn.Package
					break
				}
			}
			for _, c := range res.Calls {
				key := staleKey(c.Line, c.CalleeName)
				if _, seen := parsed[key]; seen {
					continue
				}
				id, marker := resolver.ResolveCall(c, pkg, c.FilePath)
				parsed[key] = staleResolved{id: id, marker: marker}
			}
		}
	}

	type staleRow struct {
		id   int64
		line int
		name string
	}
	srows, err := tx.Query("SELECT id, line, callee_name FROM calls WHERE resolution = ? AND file_path = ?", resStale, f)
	if err != nil {
		return err
	}
	var staleRows []staleRow
	for srows.Next() {
		var row staleRow
		if err := srows.Scan(&row.id, &row.line, &row.name); err != nil {
			srows.Close()
			return err
		}
		staleRows = append(staleRows, row)
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return err
	}

	for _, row := range staleRows {
		marker := resUnresolved
		var calleeArg interface{}
		if r, ok := parsed[staleKey(row.line, row.name)]; ok {
			marker = r.marker
			if r.id != 0 {
				calleeArg = r.id
			}
		}
		if _, err := tx.Exec("UPDATE calls SET callee_id = ?, resolution = ? WHERE id = ?", calleeArg, marker, row.id); err != nil {
			return err
		}
	}
	return nil
}

// staleKey identifies a call site within a file by line and callee name, for
// matching re-parsed calls back to their stored edge rows.
func staleKey(line int, name string) string {
	return fmt.Sprintf("%d\x00%s", line, name)
}
