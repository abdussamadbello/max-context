package indexer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/maxcontext/max-context/internal/artifacts"
	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/pkg/treesitter"
)

const batchSize = 500

// Options carries optional indexing configuration from .max-context/config.json.
// A nil *Options means defaults everywhere (all supported languages, no
// include/exclude, no size cap).
type Options struct {
	Extensions  []string // restrict to these file extensions (nil = all supported)
	Include     []string // include globs (nil = everything)
	Exclude     []string // extra exclude patterns (gitignore-style)
	MaxFileSize int64    // skip files larger than this many bytes (0 = unlimited)
	Version     string   // binary version written to generated status metadata
}

func optsOrNil(opts []*Options) *Options {
	if len(opts) > 0 {
		return opts[0]
	}
	return nil
}

// skipsFile reports whether an incrementally changed file should be ignored
// per the configured include/exclude/size rules — mirroring what Scan() would
// have excluded on a full index. excl is a matcher built from o.Exclude.
// Deleted files are never skipped here: IndexFile must still process the
// deletion to drop stale rows.
func (o *Options) skipsFile(root, relPath string, excl *IgnoreMatcher) bool {
	if o == nil {
		return false
	}
	if excl != nil && excl.Match(relPath) {
		return true
	}
	if len(o.Include) > 0 && !matchesAnyGlob(o.Include, relPath) {
		return true
	}
	if o.MaxFileSize > 0 {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(relPath))); err == nil && info.Size() > o.MaxFileSize {
			return true
		}
	}
	return false
}

// RunWorker consumes paths from ch and reindexes each file. Empty string means full reindex (Phase 4: .reindex-queue).
func RunWorker(ctx context.Context, root string, database *sql.DB, q *db.Queries, ch <-chan string, opts ...*Options) {
	o := optsOrNil(opts)
	artifactVersion := ""
	if o != nil {
		artifactVersion = o.Version
	}
	var excl *IgnoreMatcher
	if o != nil && len(o.Exclude) > 0 {
		excl = &IgnoreMatcher{patterns: o.Exclude}
	}
	// One delta-maintained resolver reused across incremental reindexes; a full
	// reindex invalidates it (it rewrites every row), so the next IndexFile
	// rebuilds it once and then maintains it per-file.
	cache := NewResolverCache()
	for {
		select {
		case <-ctx.Done():
			return
		case path, ok := <-ch:
			if !ok {
				return
			}
			if path == "" {
				if err := Index(ctx, root, database, q, o); err != nil {
					// Surface, don't swallow: a failed full index left a stale or empty
					// index that tools must be able to warn about.
					fmt.Fprintf(os.Stderr, "max-context: full index failed: %v\n", err)
					artifacts.RecordIndexError(database, "", err.Error())
					if statusErr := artifacts.WriteIndexStatus(filepath.Join(root, ".max-context"), database, artifactVersion); statusErr != nil {
						fmt.Fprintf(os.Stderr, "max-context: write failed-index status: %v\n", statusErr)
					}
				} else {
					artifacts.ClearErrorsAfterFullIndex(database)
					artifacts.SetLastFullIndex(database, time.Now())
					artifacts.ClearIndexError(database, artifacts.ArtifactErrorKey)
					if err := artifacts.WriteIndexArtifacts(filepath.Join(root, ".max-context"), database, artifactVersion); err != nil {
						fmt.Fprintf(os.Stderr, "max-context: write index artifacts failed: %v\n", err)
						artifacts.RecordIndexError(database, artifacts.ArtifactErrorKey, err.Error())
						_ = artifacts.WriteIndexStatus(filepath.Join(root, ".max-context"), database, artifactVersion)
					}
				}
				cache.Invalidate() // full reindex rewrote everything; rebuild lazily
				continue
			}
			if o.skipsFile(root, path, excl) {
				continue
			}
			// Documents take a dedicated path: no parser, no resolver, and — critically —
			// no ResolverCache invalidation on failure.
			if _, isDoc := DocKindForPath(path); isDoc {
				if err := IndexDocFile(ctx, root, path, database); err != nil {
					fmt.Fprintf(os.Stderr, "max-context: index %s failed: %v\n", path, err)
					artifacts.RecordIndexError(database, path, err.Error())
				} else {
					artifacts.ClearIndexError(database, path)
					artifacts.SetLastIncrementalIndex(database, time.Now())
				}
				continue
			}
			if err := IndexFile(ctx, root, path, database, q, cache); err != nil {
				// Surface the failure so it shows up in staleness; a bad single-file
				// reindex must not silently leave the index out of date.
				fmt.Fprintf(os.Stderr, "max-context: index %s failed: %v\n", path, err)
				artifacts.RecordIndexError(database, path, err.Error())
			} else {
				artifacts.ClearIndexError(database, path)
				artifacts.SetLastIncrementalIndex(database, time.Now())
			}
		}
	}
}

// Index runs a full index of the project at root, using the given database and queries.
func Index(ctx context.Context, root string, database *sql.DB, q *db.Queries, opts ...*Options) error {
	o := optsOrNil(opts)
	var exclude []string
	var include []string
	var extensions []string
	var maxFileSize int64
	if o != nil {
		exclude, include, extensions, maxFileSize = o.Exclude, o.Include, o.Extensions, o.MaxFileSize
	}
	ignore, err := NewIgnoreMatcherWithExtra(root, exclude)
	if err != nil {
		return fmt.Errorf("load ignore rules: %w", err)
	}
	sc := &Scanner{Root: root, Ignore: ignore, Extensions: extensions, Includes: include, MaxFileSize: maxFileSize, IncludeDocs: true}
	scanned, err := sc.Scan()
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	// Documents are indexed as plain text; only code files go through the
	// parser and resolver below.
	var files, docFiles []string
	for _, rel := range scanned {
		if _, ok := DocKindForPath(rel); ok {
			docFiles = append(docFiles, rel)
		} else {
			files = append(files, rel)
		}
	}

	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Clear existing data for full reindex. implementations references
	// functions(id), so it must go first: deleting a referenced parent row
	// fails the foreign key.
	if _, err := tx.Exec("DELETE FROM calls"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM implementations"); err != nil {
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
	if _, err := tx.Exec("DELETE FROM documents"); err != nil {
		return err
	}

	parsed, err := parseSourceFiles(ctx, root, files)
	if err != nil {
		return err
	}

	// Aggregate in scan order.
	var allFuncs []FuncRecord
	var allCalls []CallRecord
	var allTypes []TypeRecord
	var allImports []ImportRecord
	var allStructFields []StructFieldRecord
	var allPackageVars []PackageVarRecord
	var allClassBases []ClassBaseRecord

	for _, res := range parsed {
		if res == nil {
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
	for i, res := range parsed {
		if res != nil {
			if err := insertFileSummary(tx, files[i], res); err != nil {
				return err
			}
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
			"INSERT INTO types (name, name_parts, file_path, start_line, kind, definition, exported) VALUES (?1, split_identifier(?1), ?2, ?3, ?4, ?5, ?6)",
			t.Name, t.FilePath, t.Line, t.Kind, t.Definition, exported,
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
		if err := insertInterfaceDispatchEdges(tx, resolver, c, callerID); err != nil {
			return err
		}
	}

	// Record interface satisfaction as its own relation, so "what implements
	// this?" is a query rather than something inferred from the shape of a
	// caller list.
	if err := insertImplementations(tx, resolver); err != nil {
		return err
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

	// Documents: whole-file plain-text rows; FTS maintained by the V8 triggers
	// in the same transaction.
	for _, rel := range docFiles {
		if err := insertDocument(tx, root, rel); err != nil {
			return err
		}
	}

	// FTS is maintained by sync triggers (schema V6) inside this transaction, so
	// the commit flips data + FTS atomically — no separate post-commit rebuild and
	// no window where a concurrent query sees an empty FTS index.
	return tx.Commit()
}

// insertDocument reads one non-code file and writes its documents row. Read and
// encoding failures abort the transaction so a partial index is never reported
// as healthy.
func insertDocument(tx *sql.Tx, root, relPath string) error {
	kind, ok := DocKindForPath(relPath)
	if !ok {
		return nil
	}
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		return fmt.Errorf("read document %s: %w", relPath, err)
	}
	if !utf8.Valid(content) {
		return fmt.Errorf("document %s is not valid UTF-8", relPath)
	}
	_, err = tx.Exec(
		"INSERT INTO documents (file_path, title, kind, content) VALUES (?, ?, ?, ?)",
		filepath.ToSlash(relPath), docTitle(relPath, content), kind, string(content),
	)
	return err
}

// insertFileSummary records every parsed source file, including files that have
// no symbols or imports. Besides keeping the existing aggregate table useful,
// this is the authoritative inventory used by query_codebase scope=files.
func insertFileSummary(tx *sql.Tx, path string, res *ParseResult) error {
	language := "unknown"
	if lang, ok := treesitter.LanguageForPath(path); ok {
		language = string(lang)
	}
	top := make([]string, 0, 5)
	for _, fn := range res.Functions {
		top = append(top, fn.Name)
		if len(top) == 5 {
			break
		}
	}
	topJSON, err := json.Marshal(top)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT OR REPLACE INTO file_summaries
			(file_path, language, function_count, type_count, import_count, top_functions, detected_patterns)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		filepath.ToSlash(path), language, len(res.Functions), len(res.Types)+len(res.Consts), len(res.Imports), string(topJSON), "[]",
	)
	return err
}

// IndexDocFile reindexes a single non-code document (incremental). A missing
// file is treated as a deletion. Documents never touch the parser, the call
// graph, or the worker's ResolverCache — a doc change must not invalidate the
// delta-maintained resolver or re-resolve any call edges.
func IndexDocFile(ctx context.Context, root, relPath string, database *sql.DB) error {
	if _, ok := DocKindForPath(relPath); !ok {
		return nil
	}
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	path := filepath.ToSlash(relPath)
	if _, err := tx.Exec("DELETE FROM documents WHERE file_path = ?", path); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relPath))); err != nil {
		if os.IsNotExist(err) {
			return tx.Commit()
		}
		return fmt.Errorf("stat document %s: %w", relPath, err)
	}
	if err := insertDocument(tx, root, relPath); err != nil {
		return err
	}
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
		"INSERT INTO functions (name, name_parts, file_path, start_line, end_line, language, exported, code, docstring, signature, kind, receiver_type, package, return_type, symbol) VALUES (?1, split_identifier(?1), ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14)",
		f.Name, f.FilePath, f.StartLine, f.EndLine, f.Language, exported, f.Code, f.Docstring, f.Signature,
		kind, nullStr(f.ReceiverType), nullStr(f.Package), nullStr(f.ReturnType),
		nullStr(SymbolID(f.Language, f.Package, f.ReceiverType, kind, f.Name)),
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
		Line:       c.Line,
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

// interfaceReceiverType returns the interface type a call dispatches through,
// or "" when the receiver is not an interface value.
//
// A plain `var` receiver carries its own type. A `field` receiver carries the
// type of the BASE — `h.n.Send()` records Holder, not the type of n — so the
// field's declared type has to be looked up before it can be recognised as an
// interface. Gating on ReceiverKind=="var" alone silently excluded every
// interface held in a struct field, which is how most long-lived dependencies
// are actually stored.
func interfaceReceiverType(resolver *Resolver, c CallRecord) string {
	switch c.ReceiverKind {
	case "var":
		return c.ReceiverType
	case "field":
		if c.ReceiverType == "" || c.ReceiverField == "" {
			return ""
		}
		if ft, ok := resolver.fieldTypeOf([2]string{c.ReceiverType, c.ReceiverField}); ok {
			return ft
		}
	}
	return ""
}

// insertInterfaceDispatchEdges emits a low-confidence 'interface-dispatch' edge
// from the caller to each concrete implementation of an interface-method call —
// e.g. n.Send() where n is statically an interface — so get_impact can fan out
// to implementations on request. No-op for non-interface receivers, so the
// default call graph is unchanged. Called alongside the primary insertCall.
func insertInterfaceDispatchEdges(tx *sql.Tx, resolver *Resolver, c CallRecord, callerID int64) error {
	ifaceType := interfaceReceiverType(resolver, c)
	if ifaceType == "" {
		return nil
	}
	impls := resolver.interfaceMethodImpls(ifaceType, c.CalleeName)
	// The fan-out width is what separates an unambiguous dispatch from a noisy
	// one, and it is known exactly here. Recording it costs one integer per edge
	// and saves a correlated subquery inside every recursive graph walk.
	width := len(impls)
	for _, id := range impls {
		if _, err := tx.Exec(
			"INSERT INTO calls (caller_id, callee_id, callee_name, file_path, line, resolution, receiver_name, dispatch_width) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			callerID, id, c.CalleeName, c.FilePath, c.Line, resInterfaceDispatch, nullStr(c.ReceiverName), width,
		); err != nil {
			return err
		}
	}
	return nil
}

// IndexFile reindexes a single file (incremental). Call after a file change.
//
// An optional *ResolverCache (the worker passes one) keeps a delta-maintained
// resolver across calls so the per-file cost is O(changed file) instead of
// O(repo); callers that pass no cache (or a nil one) get a fresh full-scan
// resolver each call, identical in behavior.
func IndexFile(ctx context.Context, root string, relPath string, database *sql.DB, q *db.Queries, caches ...*ResolverCache) (retErr error) {
	var cache *ResolverCache
	if len(caches) > 0 {
		cache = caches[0]
	}
	// If anything fails after we touch the cached resolver, the transaction rolls
	// back but the in-memory delta does not — drop the cache so the next call
	// rebuilds it from a consistent DB.
	defer func() {
		if retErr != nil {
			cache.Invalidate()
		}
	}()

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
	// implementations.impl_func_id has the same reference, and the same failure
	// mode the comment above describes: a violation here rolls back the whole
	// reindex, so an edit to a file declaring an interface implementation would
	// silently never land.
	for _, sql := range []string{
		"DELETE FROM calls WHERE file_path = ?",
		"DELETE FROM implementations WHERE impl_func_id IN (SELECT id FROM functions WHERE file_path = ?)",
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

	// A deleted file has nothing to parse or reinsert. Refresh the resolver to
	// reflect the removal, re-resolve the edges that pointed into it, commit.
	if deleted {
		resolver, err := resolverForIndexFile(tx, cache, path, true)
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
	if err := insertFileSummary(tx, path, res); err != nil {
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
			"INSERT INTO types (name, name_parts, file_path, start_line, kind, definition, exported) VALUES (?1, split_identifier(?1), ?2, ?3, ?4, ?5, ?6)",
			t.Name, t.FilePath, t.Line, t.Kind, t.Definition, exported,
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
			"INSERT INTO types (name, name_parts, file_path, start_line, kind, definition, exported) VALUES (?1, split_identifier(?1), ?2, ?3, ?4, ?5, ?6)",
			ct.Name, ct.FilePath, ct.Line, ct.Kind, ct.Definition, exported,
		); err != nil {
			return err
		}
	}

	// Resolve against the full functions table (this file's rows were just
	// reinserted in the same tx), so same-package and receiver-typed lookups see
	// definitions in other files too. Cold: full scan; warm cache: this file's
	// delta applied to the retained resolver.
	resolver, err := resolverForIndexFile(tx, cache, path, false)
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
		if err := insertInterfaceDispatchEdges(tx, resolver, c, callerID); err != nil {
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

// resolverForIndexFile returns the resolver to use for an incremental reindex of
// path. With no cache (or a cold one) it builds a fresh full-scan resolver and
// warms the cache; with a warm cache it applies just this file's delta
// (removeFile to reflect the DELETE, addFileFromDB to reflect the new rows —
// skipped for a deletion), keeping per-file cost O(changed file).
func resolverForIndexFile(tx *sql.Tx, cache *ResolverCache, path string, deleted bool) (*Resolver, error) {
	if cache == nil || cache.r == nil {
		r, err := NewResolver(tx)
		if err != nil {
			return nil, err
		}
		if cache != nil {
			cache.r = r
		}
		return r, nil
	}
	cache.r.removeFile(path)
	if !deleted {
		if err := cache.r.addFileFromDB(tx, path); err != nil {
			return nil, err
		}
	}
	return cache.r, nil
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

// insertImplementations writes the interface-satisfaction relation.
//
// This is the same computation the dispatch fan-out already relies on, stored
// once instead of being reachable only through the synthetic edges it produces.
// It is written on a full index; incremental single-file reindexes leave it
// alone, since satisfaction is a whole-repo property that one file's methods
// can change in either direction.
func insertImplementations(tx *sql.Tx, resolver *Resolver) error {
	if _, err := tx.Exec("DELETE FROM implementations"); err != nil {
		return err
	}
	for _, rec := range resolver.Implementations() {
		if rec.ImplType == "" {
			continue // not a method this resolver indexed; storing it would name no type
		}
		if _, err := tx.Exec(
			"INSERT OR IGNORE INTO implementations (interface_type, method_name, impl_func_id, impl_type) VALUES (?, ?, ?, ?)",
			rec.InterfaceType, rec.MethodName, rec.ImplFuncID, rec.ImplType,
		); err != nil {
			return err
		}
	}
	return nil
}
