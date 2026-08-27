package indexer

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"

	"github.com/maxcontext/max-context/pkg/treesitter"
)

// FuncRecord is a function/method to be stored in the index.
type FuncRecord struct {
	Name         string
	FilePath     string
	StartLine    int
	EndLine      int
	Language     string
	Exported     bool
	Code         string
	Docstring    string
	Signature    string
	Kind         string // "func" | "method"
	ReceiverType string // e.g. "Flags" for func (f *Flags) X(); "" for plain funcs
	Package      string // the file's package name (Go); "" for languages without one
	ReturnType   string // 9a: bare single-value return type; "" otherwise
}

// CallRecord is a call edge (caller -> callee).
//
// For Go, the parser also classifies the call's receiver so the resolver can
// pick a precise target without re-deriving scope:
//   - ReceiverName: the selector operand ("db" in db.Query()); "" for bare calls
//   - ReceiverKind: "import" (operand is an imported package alias),
//     "var" (operand has a statically known type), or "" (unknown / bare call)
//   - ReceiverType: the resolved type name when ReceiverKind=="var"
//
// Phase 9 hint fields (resolver completes these against the full index):
//   - ReceiverFromCallee (9a): receiver is a local bound by `x := <callee>()`;
//     the resolver types x from that function's return_type.
//   - ReceiverField (9b): receiver is `base.field` — ReceiverType holds base's
//     type, ReceiverField holds the field name; resolver looks up the field type.
type CallRecord struct {
	CalleeName         string
	ReceiverName       string
	ReceiverKind       string
	ReceiverType       string
	ReceiverFromCallee string
	ReceiverField      string
	ImportedOrigin     string // bare call to an imported name: the ORIGINAL symbol it aliases (cross-file resolution)
	ImportedModuleRoot string // for ImportedOrigin: first path segment of the source module ("" = relative/in-repo always-allowed; else resolver checks it's a repo package root, not stdlib/3rd-party)
	ReceiverPackage    string // import-qualified call (Go pkg.Func): candidate package name (last path segment) for cross-package resolution
	Language           string
	FilePath           string
	Line               int
	CallerID           int64
}

// StructFieldRecord maps a struct field to its (bare) type. (9b)
type StructFieldRecord struct {
	StructType string
	FieldName  string
	FieldType  string
	Package    string
	FilePath   string
}

// PackageVarRecord maps a package-level var to its (bare) type. (9b)
type PackageVarRecord struct {
	Name     string
	VarType  string
	Package  string
	FilePath string
}

// ClassBaseRecord maps a class to one of its base classes, so the resolver can
// resolve calls to inherited methods (self.<inherited>()). (inheritance support)
type ClassBaseRecord struct {
	ClassName string
	BaseName  string
	Package   string
	FilePath  string
}

// ConstRecord is a module/package-level constant or variable, indexed as a
// SEARCHABLE symbol (query_codebase + get_definition) so config defaults like
// DEFAULT_TIMEOUT_CONFIG are findable. Distinct from PackageVarRecord, which is
// a type-resolution helper, not a searchable symbol.
type ConstRecord struct {
	Name     string
	FilePath string
	Line     int
}

// TypeRecord is a type declaration.
type TypeRecord struct {
	Name       string
	FilePath   string
	Line       int // 1-based declaration line; 0 when the grammar gives no position
	Kind       string
	Definition string
	Exported   bool
}

// ImportRecord is an import relationship.
type ImportRecord struct {
	FilePath        string
	ImportedPath    string
	ImportedSymbols string
}

// importedSym is a named import bound to a local name: origin is the original
// exported symbol, root is "" for relative/in-repo-guaranteed specifiers or the
// first path segment of a bare specifier (gated by the resolver against repo
// package roots, so a stdlib/3rd-party name never links to a local symbol).
type importedSym struct {
	origin string
	root   string
}

// ParseResult holds all extracted records for one file.
type ParseResult struct {
	Functions    []FuncRecord
	Calls        []CallRecord
	Types        []TypeRecord
	Imports      []ImportRecord
	StructFields []StructFieldRecord
	PackageVars  []PackageVarRecord
	ClassBases   []ClassBaseRecord
	Consts       []ConstRecord
}

// ParseFile parses the file content and returns extracted symbols and edges.
func ParseFile(ctx context.Context, filePath string, content []byte) (*ParseResult, error) {
	lang, ok := treesitter.LanguageForPath(filePath)
	if !ok {
		return &ParseResult{}, nil
	}

	content = unwrapOuterMarkdownFence(content)
	tree, err := treesitter.Parse(ctx, content, lang)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	groups, err := treesitter.RunQuery(tree, content, lang)
	if err != nil {
		return nil, err
	}

	slashPath := filepath.ToSlash(filePath)
	switch lang {
	case treesitter.LangGo:
		return parseGo(slashPath, groups), nil
	case treesitter.LangTypeScript, treesitter.LangTSX:
		return parseTS(slashPath, string(lang), groups), nil
	case treesitter.LangPython:
		return parsePython(slashPath, groups), nil
	default:
		return parseGeneric(slashPath, string(lang), groups), nil
	}
}

// unwrapOuterMarkdownFence accepts source files exported as one Markdown code
// block. Replacing only the fence lines preserves every source line number,
// while allowing Tree-sitter to index otherwise valid code. Normal source and
// files containing interior fenced examples are unchanged.
func unwrapOuterMarkdownFence(content []byte) []byte {
	lines := bytes.Split(content, []byte("\n"))
	first, last := -1, -1
	for i, line := range lines {
		if len(bytes.TrimSpace(line)) != 0 {
			first = i
			break
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if len(bytes.TrimSpace(lines[i])) != 0 {
			last = i
			break
		}
	}
	if first < 0 || last <= first || !bytes.HasPrefix(bytes.TrimSpace(lines[first]), []byte("```")) ||
		!bytes.Equal(bytes.TrimSpace(lines[last]), []byte("```")) {
		return content
	}
	copyLines := append([][]byte(nil), lines...)
	copyLines[first] = nil
	copyLines[last] = nil
	return bytes.Join(copyLines, []byte("\n"))
}

// parseGeneric handles every language except Go. It uses the legacy capture
// names (name/body/callee/definition/path/symbol) and now derives line numbers
// from real node positions instead of searching file content. Call edges carry
// no receiver information, so they resolve via the name-global fallback.
func parseGeneric(slashPath, lang string, groups []treesitter.CaptureGroup) *ParseResult {
	res := &ParseResult{}
	for _, g := range groups {
		t := g.Text
		if name, ok := t["name"]; ok && name != "" {
			if _, hasBody := t["body"]; hasBody {
				start, end := defRange(g, "name", "body")
				res.Functions = append(res.Functions, FuncRecord{
					Name:      name,
					FilePath:  slashPath,
					StartLine: start,
					EndLine:   end,
					Language:  lang,
					Exported:  isExported(name),
					Code:      truncate(t["body"], 500),
					Signature: name,
					Kind:      "func",
				})
			}
		}
		if callee, ok := t["callee"]; ok && callee != "" {
			res.Calls = append(res.Calls, CallRecord{
				CalleeName: callee,
				Language:   lang,
				FilePath:   slashPath,
				Line:       rowOf(g, "callee"),
			})
		}
		if name, ok := t["name"]; ok && name != "" {
			if def, hasDef := t["definition"]; hasDef && def != "" {
				res.Types = append(res.Types, TypeRecord{
					Name:       name,
					FilePath:   slashPath,
					Line:       rowOf(g, "name"),
					Kind:       "type",
					Definition: truncate(def, 300),
					Exported:   isExported(name),
				})
			}
		}
		if path, ok := t["path"]; ok && path != "" {
			res.Imports = append(res.Imports, ImportRecord{
				FilePath:        slashPath,
				ImportedPath:    strings.Trim(path, "\"'`"),
				ImportedSymbols: t["symbol"],
			})
		}
	}
	return res
}

// funcSpan is an enclosing function's line range plus the locally-typed
// identifiers visible within it (parameters, the method receiver, and
// statically-typed locals). Used to classify a call's receiver.
type funcSpan struct {
	start, end int
	types      map[string]string // identifier -> type name (statically known)
	fromCallee map[string]string // identifier -> callee name for `x := callee()` (9a)
}

// parseGo handles Go with receiver-aware call extraction and per-file scope.
// It classifies each selector call's receiver against the file's imports and
// the enclosing function's locally-typed identifiers.
func parseGo(slashPath string, groups []treesitter.CaptureGroup) *ParseResult {
	res := &ParseResult{}

	var pkg string
	imports := map[string]string{} // alias -> import path

	var spans []*funcSpan
	type rawCall struct {
		callee, recv string
		line         int
	}
	type rawFieldCall struct {
		base, field, callee string
		line                int
	}
	type rawTyped struct {
		name, typ string
		line      int
	}
	type rawCalleeLocal struct {
		name, callee string
		line         int
	}
	var calls []rawCall
	var fieldCalls []rawFieldCall
	var typed []rawTyped              // params, receivers, and statically-typed locals
	var calleeLocals []rawCalleeLocal // x := f() locals (9a), typed by the resolver

	for _, g := range groups {
		t := g.Text
		switch {
		case t["package"] != "":
			pkg = t["package"]

		case t["import.path"] != "":
			path := strings.Trim(t["import.path"], "\"'`")
			alias := t["import.alias"]
			if alias == "" {
				alias = lastPathSegment(path)
			}
			imports[alias] = path

		case t["func.name"] != "" && hasKey(t, "func.body"):
			start, end := defRange(g, "func.name", "func.body")
			res.Functions = append(res.Functions, FuncRecord{
				Name:       t["func.name"],
				FilePath:   slashPath,
				StartLine:  start,
				EndLine:    end,
				Language:   string(treesitter.LangGo),
				Exported:   isExported(t["func.name"]),
				Code:       truncate(t["func.body"], 500),
				Signature:  t["func.name"],
				Kind:       "func",
				ReturnType: t["func.result.type"],
			})
			spans = append(spans, &funcSpan{start: start, end: end, types: map[string]string{}})

		case t["method.name"] != "" && hasKey(t, "func.body"):
			start, end := defRange(g, "method.name", "func.body")
			res.Functions = append(res.Functions, FuncRecord{
				Name:         t["method.name"],
				FilePath:     slashPath,
				StartLine:    start,
				EndLine:      end,
				Language:     string(treesitter.LangGo),
				Exported:     isExported(t["method.name"]),
				Code:         truncate(t["func.body"], 500),
				Signature:    t["method.name"],
				Kind:         "method",
				ReceiverType: t["method.recv.type"],
				ReturnType:   t["func.result.type"],
			})
			span := &funcSpan{start: start, end: end, types: map[string]string{}}
			if rn := t["method.recv.name"]; rn != "" && t["method.recv.type"] != "" {
				span.types[rn] = t["method.recv.type"]
			}
			spans = append(spans, span)

		case t["param.name"] != "" && t["param.type"] != "":
			typed = append(typed, rawTyped{name: t["param.name"], typ: t["param.type"], line: rowOf(g, "param.name")})

		case t["local.name"] != "" && t["local.type"] != "":
			typed = append(typed, rawTyped{name: t["local.name"], typ: t["local.type"], line: rowOf(g, "local.name")})

		case t["localcall.name"] != "" && t["localcall.callee"] != "":
			calleeLocals = append(calleeLocals, rawCalleeLocal{
				name:   t["localcall.name"],
				callee: t["localcall.callee"],
				line:   rowOf(g, "localcall.name"),
			})

		case t["callfield.callee"] != "":
			fieldCalls = append(fieldCalls, rawFieldCall{
				base:   t["callfield.base"],
				field:  t["callfield.recv"],
				callee: t["callfield.callee"],
				line:   rowOf(g, "callfield.callee"),
			})

		case t["const.name"] != "":
			// Package-level const/var (source_file-anchored) -> searchable symbol.
			res.Consts = append(res.Consts, ConstRecord{
				Name: t["const.name"], FilePath: slashPath, Line: rowOf(g, "const.name"),
			})

		case t["call.callee"] != "":
			calls = append(calls, rawCall{callee: t["call.callee"], recv: t["call.recv"], line: rowOf(g, "call.callee")})

		case t["struct.name"] != "" && t["field.name"] != "" && t["field.type"] != "":
			res.StructFields = append(res.StructFields, StructFieldRecord{
				StructType: t["struct.name"],
				FieldName:  t["field.name"],
				FieldType:  t["field.type"],
				FilePath:   slashPath,
			})

		case t["type.name"] != "" && t["type.def"] != "":
			res.Types = append(res.Types, TypeRecord{
				Name:       t["type.name"],
				FilePath:   slashPath,
				Line:       rowOf(g, "type.name"),
				Kind:       "type",
				Definition: truncate(t["type.def"], 300),
				Exported:   isExported(t["type.name"]),
			})
		}
	}

	// Stamp package on every function, struct-field, and package var.
	for i := range res.Functions {
		res.Functions[i].Package = pkg
	}
	for i := range res.StructFields {
		res.StructFields[i].Package = pkg
	}
	for _, path := range imports {
		res.Imports = append(res.Imports, ImportRecord{
			FilePath:     slashPath,
			ImportedPath: path,
		})
	}

	// Assign each statically-typed identifier to its enclosing function span.
	for _, ty := range typed {
		if s := enclosing(spans, ty.line); s != nil {
			s.types[ty.name] = ty.typ
		}
	}
	// Package-level statically-typed vars (var x T at file scope) -> package_vars.
	for _, ty := range typed {
		if enclosing(spans, ty.line) == nil {
			res.PackageVars = append(res.PackageVars, PackageVarRecord{
				Name:     ty.name,
				VarType:  ty.typ,
				Package:  pkg,
				FilePath: slashPath,
			})
		}
	}

	// Map each `x := f()` local to its callee within its enclosing function, so
	// the resolver can type x from f's return_type (9a). Stored per-span.
	for _, cl := range calleeLocals {
		if s := enclosing(spans, cl.line); s != nil {
			if s.fromCallee == nil {
				s.fromCallee = map[string]string{}
			}
			s.fromCallee[cl.name] = cl.callee
		}
	}

	// Build bare/selector-ident call edges.
	for _, c := range calls {
		cr := CallRecord{
			CalleeName:   c.callee,
			ReceiverName: c.recv,
			Language:     string(treesitter.LangGo),
			FilePath:     slashPath,
			Line:         c.line,
		}
		if c.recv != "" {
			switch {
			case imports[c.recv] != "":
				cr.ReceiverKind = "import"
				// Candidate package = last segment of the import path (the
				// conventional Go package name). The resolver links pkg.Func()
				// to that package's Func only on a unique match; otherwise the
				// call stays import-qualified (no false edge if the name differs).
				cr.ReceiverPackage = lastPathSegment(strings.Trim(imports[c.recv], "\"'`"))
			default:
				s := enclosing(spans, c.line)
				if s != nil {
					if typ, ok := s.types[c.recv]; ok {
						cr.ReceiverKind = "var"
						cr.ReceiverType = typ
					} else if callee, ok := s.fromCallee[c.recv]; ok {
						// 9a: x := callee(); x.M() — resolver types x from callee's return.
						cr.ReceiverKind = "from-callee"
						cr.ReceiverFromCallee = callee
					}
				}
				// 9b: package-global receiver (no local binding found).
				if cr.ReceiverKind == "" {
					cr.ReceiverKind = "maybe-global"
				}
			}
		}
		res.Calls = append(res.Calls, cr)
	}

	// Build field-receiver call edges (9b): base.field.M().
	for _, fc := range fieldCalls {
		cr := CallRecord{
			CalleeName:    fc.callee,
			ReceiverName:  fc.base + "." + fc.field,
			ReceiverField: fc.field,
			Language:      string(treesitter.LangGo),
			FilePath:      slashPath,
			Line:          fc.line,
		}
		// Type the base; the resolver then looks up base-type's field type.
		if s := enclosing(spans, fc.line); s != nil {
			if typ, ok := s.types[fc.base]; ok {
				cr.ReceiverKind = "field"
				cr.ReceiverType = typ // base's type; ReceiverField names the field
			}
		}
		if cr.ReceiverKind == "" {
			cr.ReceiverKind = "unresolved-field"
		}
		res.Calls = append(res.Calls, cr)
	}

	return res
}

// classSpan is a class's line range plus its field-type map, used to resolve
// `this`/`self` receivers and field types within methods.
type classSpan struct {
	name       string
	start, end int
}

// parseTS handles TypeScript and TSX with annotation-based receiver typing.
// The scope key is the file path (TS modules are files). Methods resolve `this`
// against the enclosing class (tracked by line range); field/param/local types
// come from annotations; `new X()` types a local directly.
func parseTS(slashPath, lang string, groups []treesitter.CaptureGroup) *ParseResult {
	res := &ParseResult{}

	imports := map[string]string{} // namespace alias -> module path
	// importedSymbols maps a LOCAL name (the identifier a call uses) to the ORIGINAL
	// symbol for `import { f }` (f->f) and `import { f as g }` (g->f), so a bare call
	// to the local name resolves cross-file to the origin (incl. renamed imports).
	// root is "" for relative specifiers (always in-repo) or the first path segment
	// of a bare specifier (resolver gates it against repo package roots).
	importedSymbols := map[string]importedSym{}
	var fnSpans []*funcSpan
	var clsSpans []*classSpan

	type rawCall struct {
		callee, recv string
		line         int
	}
	type rawThisCall struct {
		field, callee string
		line          int
	}
	type rawTyped struct {
		name, typ string
		line      int
	}
	type rawCalleeLocal struct {
		name, callee string
		line         int
	}
	type rawSelfMethod struct {
		callee string
		line   int
	}
	var calls []rawCall
	var thisCalls []rawThisCall
	var typed []rawTyped
	var calleeLocals []rawCalleeLocal
	var selfMethods []rawSelfMethod

	for _, g := range groups {
		t := g.Text
		switch {
		case t["import.path"] != "":
			path := strings.Trim(t["import.path"], "\"'`")
			if alias := t["import.alias"]; alias != "" {
				imports[alias] = path
			}

		case t["import.symbol"] != "":
			// `import { f }` (local f) or `import { f as g }` (local g -> origin f).
			origin := t["import.symbol"]
			local := origin
			if a := t["import.alias.local"]; a != "" {
				local = a
			}
			// Gate on the source module: relative specifiers ("./..", "..") are
			// always in-repo (root=""); bare specifiers carry their first segment
			// for the resolver to check against repo roots (so a name from node:fs
			// or a 3rd-party pkg won't link to a same-named local function).
			spec := strings.Trim(t["import.symbol.src"], "\"'`")
			if local != "" && spec != "" {
				importedSymbols[local] = importedSym{origin: origin, root: tsModuleRoot(spec)}
			}

		case t["class.name"] != "" && hasKey(t, "class.body"):
			start, end := defRange(g, "class.name", "class.body")
			clsSpans = append(clsSpans, &classSpan{name: t["class.name"], start: start, end: end})
			res.Types = append(res.Types, TypeRecord{
				Name: t["class.name"], FilePath: slashPath, Line: start, Kind: "class",
				Exported: isExported(t["class.name"]),
			})

		case t["func.name"] != "" && hasKey(t, "func.body"):
			start, end := defRange(g, "func.name", "func.body")
			res.Functions = append(res.Functions, FuncRecord{
				Name: t["func.name"], FilePath: slashPath, StartLine: start, EndLine: end,
				Language: lang, Exported: true, Code: truncate(t["func.body"], 500),
				Signature: t["func.name"], Kind: "func", ReturnType: t["func.result.type"],
			})
			fnSpans = append(fnSpans, &funcSpan{start: start, end: end, types: map[string]string{}})

		case t["method.name"] != "" && hasKey(t, "func.body"):
			start, end := defRange(g, "method.name", "func.body")
			// ReceiverType (enclosing class) is filled in a post-pass once all
			// class spans are known, since capture order isn't guaranteed.
			res.Functions = append(res.Functions, FuncRecord{
				Name: t["method.name"], FilePath: slashPath, StartLine: start, EndLine: end,
				Language: lang, Exported: true, Code: truncate(t["func.body"], 500),
				Signature: t["method.name"], Kind: "method",
				ReturnType: t["func.result.type"],
			})
			fnSpans = append(fnSpans, &funcSpan{start: start, end: end, types: map[string]string{}})

		case t["field.name"] != "" && t["field.type"] != "":
			// Field type, attributed to the enclosing class below (by line range).
			typed = append(typed, rawTyped{name: "\x00field\x00" + t["field.name"], typ: t["field.type"], line: rowOf(g, "field.name")})

		case t["param.name"] != "" && t["param.type"] != "":
			typed = append(typed, rawTyped{name: t["param.name"], typ: t["param.type"], line: rowOf(g, "param.name")})

		case t["local.name"] != "" && t["local.type"] != "":
			typed = append(typed, rawTyped{name: t["local.name"], typ: t["local.type"], line: rowOf(g, "local.name")})

		case t["localnew.name"] != "" && t["localnew.type"] != "":
			typed = append(typed, rawTyped{name: t["localnew.name"], typ: t["localnew.type"], line: rowOf(g, "localnew.name")})

		case t["localcall.name"] != "" && t["localcall.callee"] != "":
			calleeLocals = append(calleeLocals, rawCalleeLocal{name: t["localcall.name"], callee: t["localcall.callee"], line: rowOf(g, "localcall.name")})

		case t["callthis.callee"] != "":
			thisCalls = append(thisCalls, rawThisCall{field: t["callthis.field"], callee: t["callthis.callee"], line: rowOf(g, "callthis.callee")})

		case t["callselfmethod.callee"] != "":
			selfMethods = append(selfMethods, rawSelfMethod{callee: t["callselfmethod.callee"], line: rowOf(g, "callselfmethod.callee")})

		case t["const.name"] != "":
			// Module-level const/let (program/export-anchored) -> searchable symbol.
			res.Consts = append(res.Consts, ConstRecord{
				Name: t["const.name"], FilePath: slashPath, Line: rowOf(g, "const.name"),
			})

		case t["call.callee"] != "":
			calls = append(calls, rawCall{callee: t["call.callee"], recv: t["call.recv"], line: rowOf(g, "call.callee")})

		case t["type.name"] != "" && t["type.def"] != "":
			res.Types = append(res.Types, TypeRecord{
				Name: t["type.name"], FilePath: slashPath, Line: rowOf(g, "type.name"), Kind: "type",
				Definition: truncate(t["type.def"], 300), Exported: isExported(t["type.name"]),
			})
		}
	}

	// Scope key = file path; stamp it where the resolver expects "package".
	// Also attribute each method to its enclosing class (-> receiver_type), so
	// it is keyed by (class, name) in byRecvName like Go methods.
	scope := slashPath
	for i := range res.Functions {
		res.Functions[i].Package = scope
		if res.Functions[i].Kind == "method" && res.Functions[i].ReceiverType == "" {
			if cs := enclosingClass(clsSpans, res.Functions[i].StartLine); cs != nil {
				res.Functions[i].ReceiverType = cs.name
			}
		}
	}
	for _, path := range imports {
		res.Imports = append(res.Imports, ImportRecord{FilePath: slashPath, ImportedPath: path})
	}

	// Attribute fields to their enclosing class, params/locals to their function.
	for _, ty := range typed {
		if strings.HasPrefix(ty.name, "\x00field\x00") {
			fieldName := strings.TrimPrefix(ty.name, "\x00field\x00")
			if cs := enclosingClass(clsSpans, ty.line); cs != nil {
				res.StructFields = append(res.StructFields, StructFieldRecord{
					StructType: cs.name, FieldName: fieldName, FieldType: ty.typ,
					Package: scope, FilePath: slashPath,
				})
			}
			continue
		}
		if s := enclosing(fnSpans, ty.line); s != nil {
			s.types[ty.name] = ty.typ
		}
	}
	for _, cl := range calleeLocals {
		if s := enclosing(fnSpans, cl.line); s != nil {
			if s.fromCallee == nil {
				s.fromCallee = map[string]string{}
			}
			s.fromCallee[cl.name] = cl.callee
		}
	}

	// Bare / member-ident calls.
	for _, c := range calls {
		cr := CallRecord{CalleeName: c.callee, ReceiverName: c.recv, Language: lang, FilePath: slashPath, Line: c.line}
		if c.recv == "" {
			// Bare call to a named-imported symbol -> resolve cross-file to origin.
			if sym, ok := importedSymbols[c.callee]; ok {
				cr.ImportedOrigin = sym.origin
				cr.ImportedModuleRoot = sym.root
			}
		}
		if c.recv != "" {
			switch {
			case imports[c.recv] != "":
				cr.ReceiverKind = "import"
			default:
				s := enclosing(fnSpans, c.line)
				if s != nil {
					if typ, ok := s.types[c.recv]; ok {
						cr.ReceiverKind = "var"
						cr.ReceiverType = typ
					} else if callee, ok := s.fromCallee[c.recv]; ok {
						cr.ReceiverKind = "from-callee"
						cr.ReceiverFromCallee = callee
					}
				}
				if cr.ReceiverKind == "" {
					cr.ReceiverKind = "maybe-global"
				}
			}
		}
		res.Calls = append(res.Calls, cr)
	}

	// this.field.m() — base type is the enclosing class.
	for _, tc := range thisCalls {
		cr := CallRecord{
			CalleeName: tc.callee, ReceiverName: "this." + tc.field, ReceiverField: tc.field,
			Language: lang, FilePath: slashPath, Line: tc.line,
		}
		if cs := enclosingClass(clsSpans, tc.line); cs != nil {
			cr.ReceiverKind = "field"
			cr.ReceiverType = cs.name
		} else {
			cr.ReceiverKind = "unresolved-field"
		}
		res.Calls = append(res.Calls, cr)
	}

	// this.m() — direct method on the enclosing class.
	for _, sm := range selfMethods {
		cr := CallRecord{CalleeName: sm.callee, ReceiverName: "this", Language: lang, FilePath: slashPath, Line: sm.line}
		if cs := enclosingClass(clsSpans, sm.line); cs != nil {
			cr.ReceiverKind = "var"
			cr.ReceiverType = cs.name
		} else {
			cr.ReceiverKind = "unresolved-field"
		}
		res.Calls = append(res.Calls, cr)
	}

	return res
}

// enclosingClass returns the innermost class span containing the given line.
func enclosingClass(spans []*classSpan, line int) *classSpan {
	var best *classSpan
	for _, s := range spans {
		if line >= s.start && line <= s.end {
			if best == nil || (s.start >= best.start && s.end <= best.end) {
				best = s
			}
		}
	}
	return best
}

// parsePython handles Python with type-hint and self.field resolution. Scope is
// the file path. `self` receivers resolve against the enclosing class; field
// types come from class-level annotations or `self.x = Ctor()` in __init__;
// param/return types come from hints. Untyped receivers stay unresolved.
func parsePython(slashPath string, groups []treesitter.CaptureGroup) *ParseResult {
	const lang = "python"
	res := &ParseResult{}

	imports := map[string]string{} // module dotted-name -> path (self == itself)
	// importedSymbols maps a LOCAL name (the bare identifier a call uses) to the
	// ORIGINAL symbol name it refers to, for `from m import f [as g]`. For a plain
	// import the local name == origin (f->f); for an alias it differs (g->f). A
	// bare call to that local name resolves cross-file to the original symbol.
	// root is "" for relative imports (from .mod — always in-repo) or the first
	// dotted segment of an absolute module (gated against repo roots by the resolver
	// so a stdlib import like `from urllib.parse import unquote` won't link locally).
	importedSymbols := map[string]importedSym{}
	var fnSpans []*funcSpan
	var clsSpans []*classSpan

	type rawCall struct {
		callee, recv string
		line         int
	}
	type rawSelfCall struct {
		field, callee string
		line          int
	}
	type rawTyped struct {
		name, typ string
		line      int
	}
	type rawField struct {
		name, typ string
		line      int
	} // class-level x: T
	type rawSelfAssign struct {
		field, ctor string
		line        int
	} // self.x = Ctor()
	type rawCalleeLocal struct {
		name, callee string
		line         int
	}
	type rawSelfMethod struct {
		callee string
		line   int
	}
	var calls []rawCall
	var selfCalls []rawSelfCall
	var typed []rawTyped
	var fields []rawField
	var selfAssigns []rawSelfAssign
	var calleeLocals []rawCalleeLocal
	var selfMethods []rawSelfMethod

	for _, g := range groups {
		t := g.Text
		switch {
		case t["import.symbol"] != "":
			// `from m import f` (alias=="" -> local name f) or
			// `from m import f as g` (alias g -> origin f). Map local -> origin.
			origin := t["import.symbol"]
			local := origin
			if a := t["import.alias"]; a != "" {
				local = a
			}
			// Gate on the source module: relative imports (relmod, e.g. `.utils`)
			// are always in-repo (root=""); absolute modules carry their first
			// dotted segment for the resolver to check against repo roots (so a
			// stdlib import like `from urllib.parse import unquote` won't link).
			root := "" // relmod -> in-repo
			if mod := t["import.symbol.mod"]; mod != "" {
				root = firstDotSegment(mod) // absolute module: gate by repo root
			}
			if local != "" {
				importedSymbols[local] = importedSym{origin: origin, root: root}
			}

		case t["path"] != "":
			p := strings.Trim(t["path"], "\"'`")
			imports[p] = p

		case t["class.name"] != "" && hasKey(t, "class.body"):
			start, end := defRange(g, "class.name", "class.body")
			clsSpans = append(clsSpans, &classSpan{name: t["class.name"], start: start, end: end})
			res.Types = append(res.Types, TypeRecord{
				Name: t["class.name"], FilePath: slashPath, Kind: "class", Exported: true,
			})

		case t["classbase.name"] != "" && t["classbase.base"] != "":
			res.ClassBases = append(res.ClassBases, ClassBaseRecord{
				ClassName: t["classbase.name"], BaseName: t["classbase.base"], FilePath: slashPath,
			})

		case t["const.name"] != "":
			// Module-level constant/var (anchored at (module ...) in the query, so
			// already file-scoped). Indexed as a searchable symbol.
			res.Consts = append(res.Consts, ConstRecord{
				Name: t["const.name"], FilePath: slashPath, Line: rowOf(g, "const.name"),
			})

		case t["func.name"] != "" && hasKey(t, "func.body"):
			start, end := defRange(g, "func.name", "func.body")
			// Kind/ReceiverType (enclosing class) filled in a post-pass below.
			res.Functions = append(res.Functions, FuncRecord{
				Name: t["func.name"], FilePath: slashPath, StartLine: start, EndLine: end,
				Language: lang, Exported: true, Code: truncate(t["func.body"], 500),
				Signature: t["func.name"], Kind: "func",
				ReturnType: t["func.result.type"],
			})
			fnSpans = append(fnSpans, &funcSpan{start: start, end: end, types: map[string]string{}})

		case t["param.name"] != "" && t["param.type"] != "":
			typed = append(typed, rawTyped{name: t["param.name"], typ: t["param.type"], line: rowOf(g, "param.name")})

		case t["field.name"] != "" && t["field.type"] != "":
			fields = append(fields, rawField{name: t["field.name"], typ: t["field.type"], line: rowOf(g, "field.name")})

		case t["selfassign.field"] != "" && t["selfassign.ctor"] != "" && t["selfassign.recv"] == "self":
			selfAssigns = append(selfAssigns, rawSelfAssign{field: t["selfassign.field"], ctor: t["selfassign.ctor"], line: rowOf(g, "selfassign.field")})

		case t["localcall.name"] != "" && t["localcall.callee"] != "":
			calleeLocals = append(calleeLocals, rawCalleeLocal{name: t["localcall.name"], callee: t["localcall.callee"], line: rowOf(g, "localcall.name")})

		case t["callself.callee"] != "":
			selfCalls = append(selfCalls, rawSelfCall{field: t["callself.field"], callee: t["callself.callee"], line: rowOf(g, "callself.callee")})

		case t["callselfmethod.callee"] != "" && t["callselfmethod.recv"] == "self":
			selfMethods = append(selfMethods, rawSelfMethod{callee: t["callselfmethod.callee"], line: rowOf(g, "callselfmethod.callee")})

		case t["call.callee"] != "":
			// Skip self.X() here — handled by the self-method/self-field cases so
			// it isn't double-counted as an unknown member call.
			if t["call.recv"] == "self" {
				continue
			}
			calls = append(calls, rawCall{callee: t["call.callee"], recv: t["call.recv"], line: rowOf(g, "call.callee")})
		}
	}

	scope := slashPath
	// A function nested in a class is a method keyed by (class, name); Python has
	// no syntactic receiver, so membership is by enclosing-class line range.
	for i := range res.Functions {
		res.Functions[i].Package = scope
		if cs := enclosingClass(clsSpans, res.Functions[i].StartLine); cs != nil {
			res.Functions[i].Kind = "method"
			res.Functions[i].ReceiverType = cs.name
		}
	}
	for _, path := range imports {
		res.Imports = append(res.Imports, ImportRecord{FilePath: slashPath, ImportedPath: path})
	}

	// Class fields: annotation `x: T` (attributed to enclosing class) and
	// `self.x = Ctor()` in a method (attributed to the method's class).
	for _, f := range fields {
		if cs := enclosingClass(clsSpans, f.line); cs != nil {
			res.StructFields = append(res.StructFields, StructFieldRecord{
				StructType: cs.name, FieldName: f.name, FieldType: f.typ, Package: scope, FilePath: slashPath,
			})
		}
	}
	for _, sa := range selfAssigns {
		if cs := enclosingClass(clsSpans, sa.line); cs != nil {
			res.StructFields = append(res.StructFields, StructFieldRecord{
				StructType: cs.name, FieldName: sa.field, FieldType: sa.ctor, Package: scope, FilePath: slashPath,
			})
		}
	}

	for _, ty := range typed {
		if s := enclosing(fnSpans, ty.line); s != nil {
			s.types[ty.name] = ty.typ
		}
	}
	for _, cl := range calleeLocals {
		if s := enclosing(fnSpans, cl.line); s != nil {
			if s.fromCallee == nil {
				s.fromCallee = map[string]string{}
			}
			s.fromCallee[cl.name] = cl.callee
		}
	}

	for _, c := range calls {
		cr := CallRecord{CalleeName: c.callee, ReceiverName: c.recv, Language: lang, FilePath: slashPath, Line: c.line}
		if c.recv == "" {
			// Bare call: if the name was imported from another module, record the
			// original symbol so the resolver can link cross-file (incl. aliases).
			if sym, ok := importedSymbols[c.callee]; ok {
				cr.ImportedOrigin = sym.origin
				cr.ImportedModuleRoot = sym.root
			}
		}
		if c.recv != "" {
			switch {
			case imports[c.recv] != "":
				cr.ReceiverKind = "import"
			default:
				s := enclosing(fnSpans, c.line)
				if s != nil {
					if typ, ok := s.types[c.recv]; ok {
						cr.ReceiverKind = "var"
						cr.ReceiverType = typ
					} else if callee, ok := s.fromCallee[c.recv]; ok {
						cr.ReceiverKind = "from-callee"
						cr.ReceiverFromCallee = callee
					}
				}
				if cr.ReceiverKind == "" {
					cr.ReceiverKind = "maybe-global"
				}
			}
		}
		res.Calls = append(res.Calls, cr)
	}

	// self.field.method() — base type is the enclosing class.
	for _, sc := range selfCalls {
		cr := CallRecord{
			CalleeName: sc.callee, ReceiverName: "self." + sc.field, ReceiverField: sc.field,
			Language: lang, FilePath: slashPath, Line: sc.line,
		}
		if cs := enclosingClass(clsSpans, sc.line); cs != nil {
			cr.ReceiverKind = "field"
			cr.ReceiverType = cs.name
		} else {
			cr.ReceiverKind = "unresolved-field"
		}
		res.Calls = append(res.Calls, cr)
	}

	// self.method() — direct method on the enclosing class.
	for _, sm := range selfMethods {
		cr := CallRecord{CalleeName: sm.callee, ReceiverName: "self", Language: lang, FilePath: slashPath, Line: sm.line}
		if cs := enclosingClass(clsSpans, sm.line); cs != nil {
			cr.ReceiverKind = "var"
			cr.ReceiverType = cs.name
		} else {
			cr.ReceiverKind = "unresolved-field"
		}
		res.Calls = append(res.Calls, cr)
	}

	return res
}

// enclosing returns the innermost function span containing the given line, or
// nil if the line is at file scope (e.g. a package-level var initializer call).
func enclosing(spans []*funcSpan, line int) *funcSpan {
	var best *funcSpan
	for _, s := range spans {
		if line >= s.start && line <= s.end {
			if best == nil || (s.start >= best.start && s.end <= best.end) {
				best = s
			}
		}
	}
	return best
}

// defRange returns the 1-based [start,end] line range of a definition, using
// the name node for the start and the body node's extent for the end.
func defRange(g treesitter.CaptureGroup, nameKey, bodyKey string) (start, end int) {
	start = rowOf(g, nameKey)
	bodyStart := rowOf(g, bodyKey)
	end = bodyStart + strings.Count(g.Text[bodyKey], "\n")
	if end < start {
		end = start
	}
	return start, end
}

// rowOf returns the 1-based source line of a captured node, or 1 if absent.
func rowOf(g treesitter.CaptureGroup, key string) int {
	if p, ok := g.Pos[key]; ok {
		return int(p.Row) + 1
	}
	return 1
}

func hasKey(t map[string]string, key string) bool {
	_, ok := t[key]
	return ok
}

// lastPathSegment returns the conventional package alias for an import path
// (its final path component), matching Go's default import naming.
func lastPathSegment(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// tsModuleRoot classifies a TS import specifier for cross-file gating. Relative
// specifiers ("./x", "../x") are always in-repo -> "". Bare specifiers return
// their first path segment (e.g. "node:fs" -> "node:fs", "@vitest/utils" ->
// "@vitest", "lodash/fp" -> "lodash") for the resolver to check against repo
// package roots. node:* and known builtins return a sentinel that never matches
// a repo root, so they are dropped.
func tsModuleRoot(spec string) string {
	if strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") {
		return "" // relative / absolute-fs -> in-repo, always allowed
	}
	seg := spec
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}
	return seg
}

// firstDotSegment returns the first segment of a Python dotted module name
// ("urllib.parse" -> "urllib", "httpx._utils" -> "httpx"). Used to gate absolute
// imports against repo package roots.
func firstDotSegment(mod string) string {
	if i := strings.IndexByte(mod, '.'); i >= 0 {
		return mod[:i]
	}
	return mod
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func isExported(name string) bool {
	if name == "" {
		return false
	}
	c := name[0]
	return c >= 'A' && c <= 'Z'
}
