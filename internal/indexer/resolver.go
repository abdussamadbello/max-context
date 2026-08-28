package indexer

import (
	"database/sql"
	"regexp"
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

	// resInterfaceDispatch is a LOW-confidence synthetic edge: a call through an
	// interface method, fanned out to every concrete type whose method set
	// satisfies the interface (name match). Imprecise by design (no arity/type
	// check, may over-approximate), so get_impact excludes these by default and
	// includes them only at low min_confidence.
	resInterfaceDispatch = "interface-dispatch"
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

// funcDef is one indexed function row, as needed for resolution. name/kind/
// retType are carried so a file's contributions can be removed incrementally
// (delta maintenance) without re-querying the whole table.
type funcDef struct {
	id       int64
	file     string
	pkg      string
	recvType string // "" for plain funcs
	name     string
	kind     string
	retType  string
}

// kv2 is a (2-string key, value) provenance entry for the field/global type maps.
type kv2 struct {
	key [2]string
	val string
}

// classBase is a (class, base) provenance entry for inheritance edges.
type classBase struct{ class, base string }

// Resolver resolves a call's callee to a specific function id using local,
// deterministic scope rules (package membership, receiver type, imports, plus
// 9a return-type inference and 9b field/global types).
//
// It supports incremental delta maintenance: addFileFromDB and removeFile keep
// the derived indexes in sync as single files change, so a cached resolver in
// the worker need not be rebuilt from full table scans on every save (which was
// O(repo) per keystroke). The derived lookups are identical to a fresh full
// rebuild — only the build cost differs — which the equivalence test guards.
type Resolver struct {
	byPkgName  map[[2]string][]funcDef // (package, name)      -> defs (plain funcs)
	byRecvName map[[2]string][]funcDef // (receiverType, name) -> defs (methods)
	byName     map[string][]funcDef    // name                 -> defs (legacy fallback)
	byTopName  map[string][]funcDef    // name -> top-level (non-method) defs, for cross-file import resolution

	returnType map[[2]string]string // (package, funcName) -> bare return type (9a)

	// Conflict-tracked type maps keep ALL distinct values per key with refcounts;
	// a lookup yields a type only when exactly one distinct value remains (the
	// "ambiguous -> no link" rule), and refcounts let one file's rows be removed
	// without losing a value another file still provides.
	fieldType  map[[2]string]map[string]int // (structType, field) -> fieldType -> refcount (9b)
	globalType map[[2]string]map[string]int // (package, varName)  -> varType  -> refcount (9b)
	classRefs  map[string]int               // class/struct name -> refcount
	bases      map[string][]string          // class name -> base class names (inheritance, multiset)
	repoRoots  map[string]int               // repo package root -> refcount, to gate absolute imports

	// interfaceMethods maps an interface type name to its method-name set (Go:
	// parsed from the type definition text). Used to fan interface-method calls
	// out to concrete implementations.
	interfaceMethods map[string]map[string]bool

	// implCache memoizes (interfaceType, method) -> concrete method func ids;
	// implDirty marks it stale after any method/interface change so it is rebuilt
	// on the next lookup.
	implCache map[[2]string][]int64
	implDirty bool

	// Per-file provenance, replayed by removeFile to undo a file's contributions.
	fileFuncs      map[string][]funcDef
	fileFields     map[string][]kv2
	filePkgVars    map[string][]kv2
	fileClasses    map[string][]string
	fileBases      map[string][]classBase
	fileRoots      map[string][]string
	fileInterfaces map[string][]string
}

// rowQuerier is satisfied by *sql.DB and *sql.Tx.
type rowQuerier interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
}

// ResolverCache holds a delta-maintained *Resolver reused across incremental
// reindexes so a single-file change costs O(changed file), not O(repo). The
// worker owns one; it is invalidated on full reindex (and on any error that may
// have left the in-memory resolver inconsistent with the DB) and rebuilt lazily.
type ResolverCache struct {
	r *Resolver
}

// NewResolverCache returns an empty (cold) cache; the next IndexFile builds it.
func NewResolverCache() *ResolverCache { return &ResolverCache{} }

// Invalidate drops the cached resolver so the next use rebuilds from a full scan.
func (c *ResolverCache) Invalidate() {
	if c != nil {
		c.r = nil
	}
}

func newEmptyResolver() *Resolver {
	return &Resolver{
		byPkgName:        map[[2]string][]funcDef{},
		byRecvName:       map[[2]string][]funcDef{},
		byName:           map[string][]funcDef{},
		byTopName:        map[string][]funcDef{},
		returnType:       map[[2]string]string{},
		fieldType:        map[[2]string]map[string]int{},
		globalType:       map[[2]string]map[string]int{},
		classRefs:        map[string]int{},
		bases:            map[string][]string{},
		repoRoots:        map[string]int{},
		interfaceMethods: map[string]map[string]bool{},
		implCache:        map[[2]string][]int64{},
		implDirty:        true,
		fileFuncs:        map[string][]funcDef{},
		fileFields:       map[string][]kv2{},
		filePkgVars:      map[string][]kv2{},
		fileClasses:      map[string][]string{},
		fileBases:        map[string][]classBase{},
		fileRoots:        map[string][]string{},
		fileInterfaces:   map[string][]string{},
	}
}

// NewResolver builds the resolution indexes from every function and type map
// currently in the database (read within the caller's transaction so
// freshly-inserted rows are visible). This is the cold/full build.
func NewResolver(q rowQuerier) (*Resolver, error) {
	r := newEmptyResolver()

	rows, err := q.Query("SELECT id, name, file_path, kind, receiver_type, package, return_type FROM functions")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id                     int64
			name, file, kind       string
			recvType, pkg, returnT sql.NullString
		)
		if err := rows.Scan(&id, &name, &file, &kind, &recvType, &pkg, &returnT); err != nil {
			return nil, err
		}
		r.addFunc(funcDef{id: id, file: file, pkg: pkg.String, recvType: recvType.String, name: name, kind: kind, retType: returnT.String})
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
	if err := r.loadInterfaces(q); err != nil {
		return nil, err
	}
	return r, nil
}

// addFileFromDB adds a single file's contributions to the resolver by reading
// only that file's rows (indexed by file_path), so an incremental update costs
// O(rows in the file) rather than O(repo). Call after removeFile(file) and after
// the file's new rows have been written in the current transaction.
func (r *Resolver) addFileFromDB(q rowQuerier, file string) error {
	frows, err := q.Query("SELECT id, name, kind, receiver_type, package, return_type FROM functions WHERE file_path = ?", file)
	if err != nil {
		return err
	}
	for frows.Next() {
		var (
			id              int64
			name, kind      string
			recv, pkg, retT sql.NullString
		)
		if err := frows.Scan(&id, &name, &kind, &recv, &pkg, &retT); err != nil {
			frows.Close()
			return err
		}
		r.addFunc(funcDef{id: id, file: file, pkg: pkg.String, recvType: recv.String, name: name, kind: kind, retType: retT.String})
	}
	frows.Close()
	if err := frows.Err(); err != nil {
		return err
	}

	if err := scanRows(q, "SELECT struct_type, field_name, field_type FROM struct_fields WHERE file_path = ?", file, func(rows *sql.Rows) error {
		var st, fn, ft string
		if err := rows.Scan(&st, &fn, &ft); err != nil {
			return err
		}
		r.addFieldType(file, [2]string{st, fn}, ft)
		return nil
	}); err != nil {
		return err
	}
	if err := scanRows(q, "SELECT name, var_type, package FROM package_vars WHERE file_path = ?", file, func(rows *sql.Rows) error {
		var name, vt string
		var pkg sql.NullString
		if err := rows.Scan(&name, &vt, &pkg); err != nil {
			return err
		}
		r.addGlobalType(file, [2]string{pkg.String, name}, vt)
		return nil
	}); err != nil {
		return err
	}
	if err := scanRows(q, "SELECT name FROM types WHERE file_path = ? AND kind IN ('class','struct','type')", file, func(rows *sql.Rows) error {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		r.addClass(file, name)
		return nil
	}); err != nil {
		return err
	}
	if err := scanRows(q, "SELECT class_name, base_name FROM class_bases WHERE file_path = ?", file, func(rows *sql.Rows) error {
		var cls, base string
		if err := rows.Scan(&cls, &base); err != nil {
			return err
		}
		r.addBase(file, cls, base)
		return nil
	}); err != nil {
		return err
	}
	return scanRows(q, "SELECT name, definition FROM types WHERE file_path = ? AND definition LIKE 'interface%'", file, func(rows *sql.Rows) error {
		var name string
		var def sql.NullString
		if err := rows.Scan(&name, &def); err != nil {
			return err
		}
		r.addInterface(file, name, def.String)
		return nil
	})
}

// scanRows runs a single-arg query and applies fn to each row, tolerating an
// absent table (partially-migrated DB) by treating it as empty.
func scanRows(q rowQuerier, query, arg string, fn func(*sql.Rows) error) error {
	rows, err := q.Query(query, arg)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// removeFile undoes every contribution a file previously added, by replaying its
// recorded provenance. After this the resolver is exactly as if the file had
// never been indexed.
func (r *Resolver) removeFile(file string) {
	for _, d := range r.fileFuncs[file] {
		removeDefByID(r.byName, d.name, d.id)
		if d.kind == "method" && d.recvType != "" {
			removeDefByID2(r.byRecvName, [2]string{d.recvType, d.name}, d.id)
		} else {
			key := [2]string{d.pkg, d.name}
			removeDefByID2(r.byPkgName, key, d.id)
			removeDefByID(r.byTopName, d.name, d.id)
			r.recomputeReturnType(key)
		}
	}
	delete(r.fileFuncs, file)

	for _, root := range r.fileRoots[file] {
		if r.repoRoots[root]--; r.repoRoots[root] <= 0 {
			delete(r.repoRoots, root)
		}
	}
	delete(r.fileRoots, file)

	for _, e := range r.fileFields[file] {
		decSet(r.fieldType, e.key, e.val)
	}
	delete(r.fileFields, file)

	for _, e := range r.filePkgVars[file] {
		decSet(r.globalType, e.key, e.val)
	}
	delete(r.filePkgVars, file)

	for _, name := range r.fileClasses[file] {
		if r.classRefs[name]--; r.classRefs[name] <= 0 {
			delete(r.classRefs, name)
		}
	}
	delete(r.fileClasses, file)

	for _, cb := range r.fileBases[file] {
		removeStrOnce(r.bases, cb.class, cb.base)
	}
	delete(r.fileBases, file)

	for _, name := range r.fileInterfaces[file] {
		// Single-declaration assumption: an interface name lives in one file. If
		// two files declared the same name, this drops the merged set (rare).
		delete(r.interfaceMethods, name)
	}
	delete(r.fileInterfaces, file)

	r.implDirty = true
}

// addFunc indexes one function definition and records its provenance.
func (r *Resolver) addFunc(d funcDef) {
	r.byName[d.name] = append(r.byName[d.name], d)
	if d.kind == "method" && d.recvType != "" {
		key := [2]string{d.recvType, d.name}
		r.byRecvName[key] = append(r.byRecvName[key], d)
	} else {
		key := [2]string{d.pkg, d.name}
		r.byPkgName[key] = append(r.byPkgName[key], d)
		r.byTopName[d.name] = append(r.byTopName[d.name], d)
		r.recomputeReturnType(key)
	}
	// Record repo package roots so absolute imports can be gated (a name from
	// "urllib.parse"/"node:fs" has a root absent here -> not linked).
	for _, root := range fileRepoRoots(d.file) {
		r.repoRoots[root]++
		r.fileRoots[d.file] = append(r.fileRoots[d.file], root)
	}
	r.fileFuncs[d.file] = append(r.fileFuncs[d.file], d)
	if d.kind == "method" {
		r.implDirty = true // a new/removed method changes which types satisfy interfaces
	}
}

// addInterface records an interface's method-name set, parsed from its Go type
// definition text (e.g. "interface {\n\tSend(...) error\n}"). Embedded
// interfaces and non-method lines are ignored (documented imprecision).
func (r *Resolver) addInterface(file, name, def string) {
	methods := extractInterfaceMethods(def)
	if len(methods) == 0 {
		return
	}
	set := r.interfaceMethods[name]
	if set == nil {
		set = map[string]bool{}
		r.interfaceMethods[name] = set
	}
	for _, m := range methods {
		set[m] = true
	}
	r.fileInterfaces[file] = append(r.fileInterfaces[file], name)
	r.implDirty = true
}

// loadInterfaces populates interface method sets from every interface type in
// the DB (Go: definition begins with "interface").
func (r *Resolver) loadInterfaces(q rowQuerier) error {
	rows, err := q.Query("SELECT name, definition, file_path FROM types WHERE definition LIKE 'interface%'")
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var name, file string
		var def sql.NullString
		if err := rows.Scan(&name, &def, &file); err != nil {
			return err
		}
		r.addInterface(file, name, def.String)
	}
	return rows.Err()
}

// ifaceMethodRe matches a method declaration inside a Go interface body: an
// identifier immediately followed by '(', positioned at the start of a line, or
// after the opening brace, or after a semicolon separating methods on one line.
//
// Anchoring on line start alone silently dropped every method of a single-line
// interface — `interface{ Send(msg string) error }` yielded no methods at all,
// so no concrete type ever satisfied it and the interface-dispatch fan-out was
// empty for that declaration. The two forms are identical Go; only the
// formatting differed. See experiments/eval/benchmarks/in-house/DISPATCH.md.
var ifaceMethodRe = regexp.MustCompile(`(?m)(?:^|[{;])\s*([A-Za-z_]\w*)\s*\(`)

func extractInterfaceMethods(def string) []string {
	var out []string
	for _, m := range ifaceMethodRe.FindAllStringSubmatch(def, -1) {
		out = append(out, m[1])
	}
	return out
}

// interfaceMethodImpls returns the concrete method func ids that satisfy calling
// method on a value of interface type ifaceType. Empty when ifaceType is not a
// known interface. Rebuilds the memo if a method/interface changed since last use.
func (r *Resolver) interfaceMethodImpls(ifaceType, method string) []int64 {
	if _, ok := r.interfaceMethods[ifaceType]; !ok {
		return nil
	}
	if r.implDirty {
		r.rebuildImplements()
	}
	return r.implCache[[2]string{ifaceType, method}]
}

// rebuildImplements recomputes the (interface, method) -> concrete-impl-ids memo.
// A concrete type T satisfies interface I when every method name in I is present
// in T's method set (name-only; no arity/type check — see resInterfaceDispatch).
func (r *Resolver) rebuildImplements() {
	r.implCache = map[[2]string][]int64{}
	// Method-name set per concrete receiver type, from the method index.
	typeMethods := map[string]map[string]bool{}
	for key := range r.byRecvName {
		t, m := key[0], key[1]
		s := typeMethods[t]
		if s == nil {
			s = map[string]bool{}
			typeMethods[t] = s
		}
		s[m] = true
	}
	for iface, mset := range r.interfaceMethods {
		if len(mset) == 0 {
			continue
		}
		for t, tm := range typeMethods {
			if t == iface || !subsetKeys(mset, tm) {
				continue
			}
			for m := range mset {
				for _, d := range r.byRecvName[[2]string{t, m}] {
					k := [2]string{iface, m}
					r.implCache[k] = append(r.implCache[k], d.id)
				}
			}
		}
	}
	r.implDirty = false
}

func subsetKeys(small, big map[string]bool) bool {
	for k := range small {
		if !big[k] {
			return false
		}
	}
	return true
}

// recomputeReturnType refreshes returnType[key] from the current defs at key
// (first def carrying a return type wins), so a removed/added def can't leave a
// stale entry. Bounded by the (tiny) number of same-keyed defs.
func (r *Resolver) recomputeReturnType(key [2]string) {
	delete(r.returnType, key)
	for _, d := range r.byPkgName[key] {
		if d.retType != "" {
			r.returnType[key] = d.retType
			return
		}
	}
}

func (r *Resolver) addFieldType(file string, key [2]string, ft string) {
	s := r.fieldType[key]
	if s == nil {
		s = map[string]int{}
		r.fieldType[key] = s
	}
	s[ft]++
	r.fileFields[file] = append(r.fileFields[file], kv2{key, ft})
}

func (r *Resolver) addGlobalType(file string, key [2]string, vt string) {
	s := r.globalType[key]
	if s == nil {
		s = map[string]int{}
		r.globalType[key] = s
	}
	s[vt]++
	r.filePkgVars[file] = append(r.filePkgVars[file], kv2{key, vt})
}

func (r *Resolver) addClass(file, name string) {
	r.classRefs[name]++
	r.fileClasses[file] = append(r.fileClasses[file], name)
}

func (r *Resolver) addBase(file, class, base string) {
	r.bases[class] = append(r.bases[class], base)
	r.fileBases[file] = append(r.fileBases[file], classBase{class, base})
}

// isClassName reports whether name is currently declared as a class/struct.
func (r *Resolver) isClassName(name string) bool { return r.classRefs[name] > 0 }

// fieldTypeOf returns the field's type when exactly one distinct type is known
// for (structType, field); ambiguous (conflicting) keys yield no link.
func (r *Resolver) fieldTypeOf(key [2]string) (string, bool) { return soleValue(r.fieldType[key]) }

// globalTypeOf is fieldTypeOf for package-level vars (package, varName).
func (r *Resolver) globalTypeOf(key [2]string) (string, bool) { return soleValue(r.globalType[key]) }

func soleValue(s map[string]int) (string, bool) {
	if len(s) != 1 {
		return "", false
	}
	for v := range s {
		return v, true
	}
	return "", false
}

func removeDefByID(m map[string][]funcDef, key string, id int64) {
	defs, ok := m[key]
	if !ok {
		return
	}
	filtered := defs[:0]
	for _, d := range defs {
		if d.id != id {
			filtered = append(filtered, d)
		}
	}
	if len(filtered) == 0 {
		delete(m, key)
	} else {
		m[key] = filtered
	}
}

func removeDefByID2(m map[[2]string][]funcDef, key [2]string, id int64) {
	defs, ok := m[key]
	if !ok {
		return
	}
	filtered := defs[:0]
	for _, d := range defs {
		if d.id != id {
			filtered = append(filtered, d)
		}
	}
	if len(filtered) == 0 {
		delete(m, key)
	} else {
		m[key] = filtered
	}
}

func decSet(m map[[2]string]map[string]int, key [2]string, val string) {
	s := m[key]
	if s == nil {
		return
	}
	if s[val]--; s[val] <= 0 {
		delete(s, val)
	}
	if len(s) == 0 {
		delete(m, key)
	}
}

func removeStrOnce(m map[string][]string, key, val string) {
	vs := m[key]
	for i, v := range vs {
		if v == val {
			vs = append(vs[:i], vs[i+1:]...)
			if len(vs) == 0 {
				delete(m, key)
			} else {
				m[key] = vs
			}
			return
		}
	}
}

// loadClassBases populates the class→bases inheritance map, enabling resolution
// of calls to inherited methods (self.<inherited>()).
func (r *Resolver) loadClassBases(q rowQuerier) error {
	rows, err := q.Query("SELECT class_name, base_name, file_path FROM class_bases")
	if err != nil {
		return nil // table may be absent on a partially-migrated DB; tolerate
	}
	defer rows.Close()
	for rows.Next() {
		var cls, base, file string
		if err := rows.Scan(&cls, &base, &file); err != nil {
			return err
		}
		r.addBase(file, cls, base)
	}
	return rows.Err()
}

// loadClassNames records type names declared as a class/struct, so a local
// bound by `x = Cls()` can be typed to Cls (the constructor case in languages
// where construction looks like a call, e.g. Python/TS without `new`).
func (r *Resolver) loadClassNames(q rowQuerier) error {
	rows, err := q.Query("SELECT name, file_path FROM types WHERE kind IN ('class','struct','type')")
	if err != nil {
		return nil // types table always exists post-migration; tolerate absence
	}
	defer rows.Close()
	for rows.Next() {
		var name, file string
		if err := rows.Scan(&name, &file); err != nil {
			return err
		}
		r.addClass(file, name)
	}
	return rows.Err()
}

// loadFieldTypes populates fieldType from struct_fields. A (structType, field)
// with conflicting types across files yields no link (handled at lookup via the
// distinct-value set).
func (r *Resolver) loadFieldTypes(q rowQuerier) error {
	rows, err := q.Query("SELECT struct_type, field_name, field_type, file_path FROM struct_fields")
	if err != nil {
		// struct_fields may not exist on a partially-migrated DB; treat as empty.
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var st, fn, ft, file string
		if err := rows.Scan(&st, &fn, &ft, &file); err != nil {
			return err
		}
		r.addFieldType(file, [2]string{st, fn}, ft)
	}
	return rows.Err()
}

// loadGlobalTypes populates globalType from package_vars (same conflict rule).
func (r *Resolver) loadGlobalTypes(q rowQuerier) error {
	rows, err := q.Query("SELECT name, var_type, package, file_path FROM package_vars")
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var name, vt, file string
		var pkg sql.NullString
		if err := rows.Scan(&name, &vt, &pkg, &file); err != nil {
			return err
		}
		r.addGlobalType(file, [2]string{pkg.String, name}, vt)
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
	return root == "" || r.repoRoots[root] > 0
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
		if r.isClassName(c.ReceiverFromCallee) {
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
		if ft, ok := r.fieldTypeOf([2]string{c.ReceiverType, c.ReceiverField}); ok {
			if id, ok := r.methodOnType(ft, c.CalleeName); ok {
				return id, resReceiverTyped
			}
		}
		return 0, resUnresolved

	case "maybe-global":
		// x.M() where x had no local binding — try a scope-level var.
		if vt, ok := r.globalTypeOf([2]string{scope, c.ReceiverName}); ok {
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
		if r.isClassName(c.CalleeName) {
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
