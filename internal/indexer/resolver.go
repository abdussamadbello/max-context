package indexer

import (
	"database/sql"
)

// Resolution markers, highest to lowest confidence. A non-empty calleeID is
// returned only for the linking markers; classify-only and miss markers leave
// callee_id NULL.
const (
	resSameFile        = "same-file"        // bare call, target defined in the same file
	resSamePackage     = "same-package"     // bare call, target defined elsewhere in the package
	resImportSymbol    = "import-symbol"    // bare call to a `from m import f [as g]` name — linked cross-file to f
	resReceiverTyped   = "receiver-typed"   // method call, receiver type statically known
	resCrossPackage    = "cross-package"    // Go pkg.Func() linked to Func in the imported package (unique match)
	resImportQualified = "import-qualified" // pkg.Foo() — not linked (ambiguous, or package name != path segment)
	resBuiltin         = "builtin"          // Go builtin (make, len, ...) — not a user symbol
	resConstructor     = "constructor"      // bare call to a known class/struct (Python/TS Conn()) — classified, not a missed function edge
	resNameGlobal      = "name-global"      // legacy global name lookup (non-Go / fallback)
	resUnresolved      = "unresolved"       // Go-precise lookup attempted, no confident target
	resStale           = "stale"            // edge previously resolved; its target file was reindexed (callee_id nulled), pending re-resolution within the same transaction
)

// goBuiltins are the predeclared functions that look like bare calls but are
// not user-defined symbols. Classifying them as builtin avoids false same-name
// links (e.g. a user function named "new").
var goBuiltins = map[string]bool{
	"make": true, "len": true, "cap": true, "append": true, "copy": true,
	"delete": true, "new": true, "panic": true, "recover": true, "close": true,
	"print": true, "println": true, "complex": true, "real": true, "imag": true,
	"min": true, "max": true, "clear": true,
}

// funcDef is one indexed function row, as needed for resolution.
type funcDef struct {
	id       int64
	file     string
	pkg      string
	recvType string // "" for plain funcs
}

// Resolver resolves a call's callee to a specific function id using local,
// deterministic scope rules (package membership, receiver type, imports, plus
// 9a return-type inference and 9b field/global types). It is built from the
// functions and type maps already written in the current transaction, so ids
// and metadata exist before resolution runs.
type Resolver struct {
	byPkgName  map[[2]string][]funcDef // (package, name)      -> defs (plain funcs)
	byRecvName map[[2]string][]funcDef // (receiverType, name) -> defs (methods)
	byName     map[string][]funcDef    // name                 -> defs (legacy fallback)
	byTopName  map[string][]funcDef    // name -> top-level (non-method) defs, for cross-file import resolution

	returnType map[[2]string]string // (package, funcName)   -> bare return type (9a)
	fieldType  map[[2]string]string // (structType, field)   -> bare field type (9b)
	globalType map[[2]string]string // (package, varName)    -> bare var type (9b)
	classNames map[string]bool      // type names declared as a class/struct
	bases      map[string][]string  // class name -> base class names (inheritance)
	repoRoots  map[string]bool      // first path segment of every indexed file — the repo's own package roots, to gate absolute imports (stdlib/3rd-party roots are absent)
}

// rowQuerier is satisfied by *sql.DB and *sql.Tx.
type rowQuerier interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
}

// NewResolver builds the resolution indexes from every function and type map
// currently in the database (read within the caller's transaction so
// freshly-inserted rows are visible).
func NewResolver(q rowQuerier) (*Resolver, error) {
	r := &Resolver{
		byPkgName:  map[[2]string][]funcDef{},
		byRecvName: map[[2]string][]funcDef{},
		byName:     map[string][]funcDef{},
		byTopName:  map[string][]funcDef{},
		returnType: map[[2]string]string{},
		fieldType:  map[[2]string]string{},
		globalType: map[[2]string]string{},
		classNames: map[string]bool{},
		bases:      map[string][]string{},
		repoRoots:  map[string]bool{},
	}

	rows, err := q.Query("SELECT id, name, file_path, kind, receiver_type, package, return_type FROM functions")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id                         int64
			name, file, kind           string
			recvType, pkg, returnT     sql.NullString
		)
		if err := rows.Scan(&id, &name, &file, &kind, &recvType, &pkg, &returnT); err != nil {
			return nil, err
		}
		d := funcDef{id: id, file: file, pkg: pkg.String, recvType: recvType.String}
		// Record repo package roots so absolute imports can be gated (a name from
		// "urllib.parse"/"node:fs" has a root absent here -> not linked). Two forms:
		//   - first dir segment of a nested file: "httpx/_utils.py" -> "httpx"
		//   - module basename of a top-level file: "utils.py" -> "utils"
		// (flat repos import a sibling as `from utils import f`).
		for _, root := range fileRepoRoots(file) {
			r.repoRoots[root] = true
		}
		r.byName[name] = append(r.byName[name], d)
		if kind == "method" && recvType.Valid && recvType.String != "" {
			r.byRecvName[[2]string{recvType.String, name}] = append(r.byRecvName[[2]string{recvType.String, name}], d)
		} else {
			r.byPkgName[[2]string{pkg.String, name}] = append(r.byPkgName[[2]string{pkg.String, name}], d)
			r.byTopName[name] = append(r.byTopName[name], d)
			if returnT.Valid && returnT.String != "" {
				r.returnType[[2]string{pkg.String, name}] = returnT.String
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := r.loadFieldTypes(q); err != nil {
		return nil, err
	}
	if err := r.loadGlobalTypes(q); err != nil {
		return nil, err
	}
	if err := r.loadClassNames(q); err != nil {
		return nil, err
	}
	if err := r.loadClassBases(q); err != nil {
		return nil, err
	}
	return r, nil
}

// loadClassBases populates the class→bases inheritance map, enabling resolution
// of calls to inherited methods (self.<inherited>()).
func (r *Resolver) loadClassBases(q rowQuerier) error {
	rows, err := q.Query("SELECT class_name, base_name FROM class_bases")
	if err != nil {
		return nil // table may be absent on a partially-migrated DB; tolerate
	}
	defer rows.Close()
	for rows.Next() {
		var cls, base string
		if err := rows.Scan(&cls, &base); err != nil {
			return err
		}
		r.bases[cls] = append(r.bases[cls], base)
	}
	return rows.Err()
}

// loadClassNames records type names declared as a class/struct, so a local
// bound by `x = Cls()` can be typed to Cls (the constructor case in languages
// where construction looks like a call, e.g. Python/TS without `new`).
func (r *Resolver) loadClassNames(q rowQuerier) error {
	rows, err := q.Query("SELECT DISTINCT name FROM types WHERE kind IN ('class','struct','type')")
	if err != nil {
		return nil // types table always exists post-migration; tolerate absence
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		r.classNames[name] = true
	}
	return rows.Err()
}

// loadFieldTypes populates fieldType from struct_fields. A (structType, field)
// with conflicting types across files is dropped (ambiguous) to avoid wrong links.
func (r *Resolver) loadFieldTypes(q rowQuerier) error {
	rows, err := q.Query("SELECT struct_type, field_name, field_type FROM struct_fields")
	if err != nil {
		// struct_fields may not exist on a partially-migrated DB; treat as empty.
		return nil
	}
	defer rows.Close()
	conflict := map[[2]string]bool{}
	for rows.Next() {
		var st, fn, ft string
		if err := rows.Scan(&st, &fn, &ft); err != nil {
			return err
		}
		key := [2]string{st, fn}
		if prev, ok := r.fieldType[key]; ok && prev != ft {
			conflict[key] = true
			continue
		}
		r.fieldType[key] = ft
	}
	for key := range conflict {
		delete(r.fieldType, key)
	}
	return rows.Err()
}

// loadGlobalTypes populates globalType from package_vars (same conflict rule).
func (r *Resolver) loadGlobalTypes(q rowQuerier) error {
	rows, err := q.Query("SELECT name, var_type, package FROM package_vars")
	if err != nil {
		return nil
	}
	defer rows.Close()
	conflict := map[[2]string]bool{}
	for rows.Next() {
		var name, vt string
		var pkg sql.NullString
		if err := rows.Scan(&name, &vt, &pkg); err != nil {
			return err
		}
		key := [2]string{pkg.String, name}
		if prev, ok := r.globalType[key]; ok && prev != vt {
			conflict[key] = true
			continue
		}
		r.globalType[key] = vt
	}
	for key := range conflict {
		delete(r.globalType, key)
	}
	return rows.Err()
}

// methodOnType looks up a single method definition for (recvType, name). If the
// method is not defined on recvType directly, it walks the inheritance chain
// (recvType's base classes, breadth-first) so calls to INHERITED methods resolve
// — e.g. Client(BaseClient).request calling self.build_request() links to
// BaseClient.build_request. Returns the id and true when an unambiguous match is
// found at the nearest level.
func (r *Resolver) methodOnType(recvType, name string) (int64, bool) {
	if recvType == "" {
		return 0, false
	}
	visited := map[string]bool{}
	queue := []string{recvType}
	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]
		if visited[t] {
			continue
		}
		visited[t] = true
		if defs := r.byRecvName[[2]string{t, name}]; len(defs) == 1 {
			return defs[0].id, true
		}
		// Not found on t — try its base classes next (single inheritance is the
		// common case; multiple bases are enqueued in declared order).
		queue = append(queue, r.bases[t]...)
	}
	return 0, false
}

// importInRepo reports whether a named import may seed a cross-file edge. An
// empty root means a relative specifier (always in-repo). A non-empty root must
// match a repo package root — so a name imported from a stdlib/3rd-party module
// (urllib.parse, node:fs, lodash) is never linked to a same-named local symbol.
func (r *Resolver) importInRepo(root string) bool {
	return root == "" || r.repoRoots[root]
}

// fileRepoRoots returns the import-namespace roots a file contributes: the first
// directory segment of a nested path ("httpx/_utils.py" -> "httpx") and, for a
// top-level file, its module basename without extension ("utils.py" -> "utils",
// so a flat-repo `from utils import f` is recognized as in-repo).
func fileRepoRoots(p string) []string {
	if i := indexByteASCII(p, '/'); i >= 0 {
		return []string{p[:i]}
	}
	// Top-level file: strip the extension to get the importable module name.
	base := p
	if dot := lastIndexByteASCII(base, '.'); dot >= 0 {
		base = base[:dot]
	}
	if base == "" {
		return nil
	}
	return []string{base}
}

// indexByteASCII is strings.IndexByte without importing strings here.
func indexByteASCII(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func lastIndexByteASCII(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// ResolveCall returns the resolved callee id (0 = no link) and a confidence
// marker for the given call. callerPkg and callerFile describe the call site's
// enclosing context: callerScope is the call site's scope key (package for Go,
// module/file for TS/Python), callerFile is the call site's file path.
func (r *Resolver) ResolveCall(c CallRecord, callerScope, callerFile string) (int64, string) {
	s, ok := strategies[c.Language]
	if !ok {
		s = nameGlobalStrategy{}
	}
	return s.Resolve(r, c, callerScope, callerFile)
}

// resolveStrategy resolves a call edge for one language family. Strategies share
// the lookup primitives on *Resolver (methodOnType, byPkgName, returnType,
// fieldType, globalType) and differ only in how a receiver is classified and
// which markers they emit.
type resolveStrategy interface {
	Resolve(r *Resolver, c CallRecord, callerScope, callerFile string) (int64, string)
}

// strategies maps a language to its resolver. Languages absent here (javascript,
// jsx, and anything without static type info) fall through to nameGlobalStrategy.
var strategies = map[string]resolveStrategy{
	"go":         goStrategy{},
	"typescript": tsStrategy{},
	"tsx":        tsStrategy{},
	"python":     pyStrategy{},
}

// nameGlobalStrategy is the honest fallback: a single global name lookup,
// labeled name-global. Used for JS/JSX and any unmodeled language.
type nameGlobalStrategy struct{}

func (nameGlobalStrategy) Resolve(r *Resolver, c CallRecord, _, _ string) (int64, string) {
	if id, ok := firstID(r.byName[c.CalleeName]); ok {
		return id, resNameGlobal
	}
	return 0, resNameGlobal
}

// typedReceiverStrategy holds the receiver-kind decision tree shared by all
// statically-typed languages (Go, TS, Python). Per-language strategies embed it
// and supply only what differs (builtins, the import marker policy).
type typedReceiverStrategy struct {
	builtins map[string]bool // predeclared/global functions that are never user symbols
}

// resolveTyped implements the common decision tree over CallRecord.ReceiverKind.
// scope is the same-scope key (package for Go, module/file for TS/Python).
func (t typedReceiverStrategy) resolveTyped(r *Resolver, c CallRecord, scope, file string) (int64, string) {
	switch c.ReceiverKind {
	case "import":
		// pkg.Foo(): a call into another package. Link to that package's Foo when
		// the candidate package (last import-path segment) has exactly one such
		// top-level function. Falls back to import-qualified (unlinked) when the
		// package name differs from the path segment or the match is ambiguous —
		// never a false edge.
		if c.ReceiverPackage != "" {
			if defs := r.byPkgName[[2]string{c.ReceiverPackage, c.CalleeName}]; len(defs) == 1 {
				return defs[0].id, resCrossPackage
			}
		}
		return 0, resImportQualified

	case "var":
		// Method call on a receiver whose type is statically known.
		if id, ok := r.methodOnType(c.ReceiverType, c.CalleeName); ok {
			return id, resReceiverTyped
		}
		return 0, resUnresolved

	case "from-callee":
		// x := callee(); x.M(). Two ways to type x:
		//   (a) callee is a class/struct -> x is an instance of that class
		//       (constructor call: Python `Conn()`, TS without `new`),
		//   (b) callee is a function -> x is its declared return type.
		if r.classNames[c.ReceiverFromCallee] {
			if id, ok := r.methodOnType(c.ReceiverFromCallee, c.CalleeName); ok {
				return id, resReceiverTyped
			}
			return 0, resUnresolved
		}
		if rt, ok := r.returnType[[2]string{scope, c.ReceiverFromCallee}]; ok {
			if id, ok := r.methodOnType(rt, c.CalleeName); ok {
				return id, resReceiverTyped
			}
		}
		return 0, resUnresolved

	case "field":
		// base.field.M() where base's type is known. Look up the field's type,
		// then the method on that type. (Go struct field; TS/Python this/self field.)
		if ft, ok := r.fieldType[[2]string{c.ReceiverType, c.ReceiverField}]; ok {
			if id, ok := r.methodOnType(ft, c.CalleeName); ok {
				return id, resReceiverTyped
			}
		}
		return 0, resUnresolved

	case "maybe-global":
		// x.M() where x had no local binding — try a scope-level var.
		if vt, ok := r.globalType[[2]string{scope, c.ReceiverName}]; ok {
			if id, ok := r.methodOnType(vt, c.CalleeName); ok {
				return id, resReceiverTyped
			}
		}
		return 0, resUnresolved

	case "unresolved-field":
		// base.field.M() where base's type is unknown.
		return 0, resUnresolved

	default:
		// Bare call (no receiver) or a selector whose receiver type is unknown.
		if c.ReceiverName != "" {
			return 0, resUnresolved
		}
		if t.builtins[c.CalleeName] {
			return 0, resBuiltin
		}
		// Constructor call: a bare call whose name is a known class/struct (e.g.
		// Python/TS `Conn()`). Classified, not a missed function edge.
		if r.classNames[c.CalleeName] {
			return 0, resConstructor
		}
		// Same-scope plain function; a single match is expected, ambiguity -> unresolved.
		if defs := r.byPkgName[[2]string{scope, c.CalleeName}]; len(defs) == 1 {
			marker := resSamePackage
			if defs[0].file == file {
				marker = resSameFile
			}
			return defs[0].id, marker
		}
		// Cross-file imported symbol: `from m import f [as g]`, then a bare call to
		// the local name. The import maps it to its ORIGINAL symbol; link only when
		// that origin has exactly one top-level definition (no false edges on
		// ambiguity). This is what catches aliased calls grep can't text-match.
		if c.ImportedOrigin != "" && r.importInRepo(c.ImportedModuleRoot) {
			if defs := r.byTopName[c.ImportedOrigin]; len(defs) == 1 {
				return defs[0].id, resImportSymbol
			}
		}
		return 0, resUnresolved
	}
}

// goStrategy is the Go decision tree (unchanged behavior; Go builtins).
type goStrategy struct{}

func (goStrategy) Resolve(r *Resolver, c CallRecord, scope, file string) (int64, string) {
	return typedReceiverStrategy{builtins: goBuiltins}.resolveTyped(r, c, scope, file)
}

// tsStrategy resolves TypeScript/TSX using the shared typed-receiver tree. The
// parser fills ReceiverKind from type annotations, `this.field`, `new X()`, and
// imports, so the resolver logic is identical to Go's.
type tsStrategy struct{}

func (tsStrategy) Resolve(r *Resolver, c CallRecord, scope, file string) (int64, string) {
	return typedReceiverStrategy{builtins: tsBuiltins}.resolveTyped(r, c, scope, file)
}

// pyStrategy resolves Python using the shared typed-receiver tree. The parser
// fills ReceiverKind from type hints, `self.field`, and constructor locals.
type pyStrategy struct{}

func (pyStrategy) Resolve(r *Resolver, c CallRecord, scope, file string) (int64, string) {
	return typedReceiverStrategy{builtins: pyBuiltins}.resolveTyped(r, c, scope, file)
}

// tsBuiltins are global functions that appear as bare calls but are never
// user-defined module symbols — classifying them avoids false same-module links.
var tsBuiltins = map[string]bool{
	"require": true, "parseInt": true, "parseFloat": true, "isNaN": true,
	"isFinite": true, "encodeURIComponent": true, "decodeURIComponent": true,
	"setTimeout": true, "setInterval": true, "clearTimeout": true, "clearInterval": true,
	"fetch": true, "structuredClone": true, "queueMicrotask": true,
}

// pyBuiltins are Python predeclared callables that appear as bare calls.
var pyBuiltins = map[string]bool{
	"print": true, "len": true, "range": true, "enumerate": true, "zip": true,
	"map": true, "filter": true, "sorted": true, "reversed": true, "sum": true,
	"min": true, "max": true, "abs": true, "open": true, "isinstance": true,
	"issubclass": true, "getattr": true, "setattr": true, "hasattr": true,
	"str": true, "int": true, "float": true, "bool": true, "list": true,
	"dict": true, "set": true, "tuple": true, "type": true, "super": true,
	"format": true, "repr": true, "hash": true, "iter": true, "next": true,
}

func firstID(defs []funcDef) (int64, bool) {
	if len(defs) == 0 {
		return 0, false
	}
	return defs[0].id, true
}

// CallerForLine returns the function ID that contains the given line in the file, or 0.
func CallerForLine(db *sql.DB, filePath string, line int) (int64, error) {
	return callerForLineQuery(db, filePath, line)
}

// CallerForLineTx is like CallerForLine but runs in the given transaction.
func CallerForLineTx(tx *sql.Tx, filePath string, line int) (int64, error) {
	return callerForLineQuery(tx, filePath, line)
}

type queryRower interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}

func callerForLineQuery(q queryRower, filePath string, line int) (int64, error) {
	var id int64
	err := q.QueryRow(
		"SELECT id FROM functions WHERE file_path = ? AND start_line <= ? AND end_line >= ? ORDER BY (end_line - start_line) ASC LIMIT 1",
		filePath, line, line,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}
