package indexer

import (
	"path"
	"strings"
)

// Import handling, shared across languages.
//
// Binding a local name to the symbol it was imported under is the mechanism
// behind this project's one published quality win: a call site reading
// `apply_entry(...)` reaches `post_transaction` because the import said so, and
// a text search cannot (experiments/eval/benchmarks/in-house/FINDINGS.md, v4).
// It was implemented separately in parseTS and parsePython, identically apart
// from which capture holds the alias and how a module root is derived.
//
// What genuinely differs is module syntax — a TypeScript specifier and a Python
// dotted module are not the same string — so that stays per language. What a
// local binding MEANS does not.

// recordImportedSymbol binds a local name to the symbol it was imported under.
//
// alias is the local rebinding when the import renames the symbol, and empty
// when it does not; moduleRoot gates the binding at resolution time, empty
// meaning "relative import, always in-repo". A binding with no origin is
// dropped rather than stored under "", which would match every bare call whose
// name the parser failed to read.
func recordImportedSymbol(into map[string]importedSym, origin, alias, moduleRoot string) {
	if into == nil || origin == "" {
		return
	}
	local := origin
	if alias != "" {
		local = alias
	}
	into[local] = importedSym{origin: origin, root: moduleRoot}
}

// relativeModuleFile resolves a relative module specifier to the file it names,
// in the form both parsers stamp as a definition's package: a slash path.
//
// This is what lets `ns.member()` resolve for a namespace import. Go already
// had the equivalent — its parser records a candidate package and the resolver
// links only on a unique match — while TypeScript and Python recorded the alias
// and stopped, so the identical pattern resolved in one language and not the
// others.
//
// Deliberately narrow. Only a relative specifier naming a sibling file is
// resolved: a bare specifier names a package outside this repository, and a
// directory module (`./pkg` meaning `./pkg/index.ts`) needs a filesystem the
// parser does not consult. Both return "", so the call stays unresolved rather
// than pointing at a package key that exists nowhere — the resolver's
// unique-match rule then yields no edge, which is the correct answer for an
// import this function cannot see.
func relativeModuleFile(fromFile, spec, ext string) string {
	spec = strings.Trim(spec, "\"'`")
	if spec == "" || !strings.HasPrefix(spec, ".") || ext == "" {
		return ""
	}
	base := path.Join(path.Dir(fromFile), spec)
	if base == "" || base == "." {
		return ""
	}
	// A specifier may already carry the extension (`./ledger.js` in ESM).
	if path.Ext(base) != "" {
		return base
	}
	return base + ext
}

// pythonModuleFile resolves a Python module name to the file it names. A
// leading dot is a relative import counted from the importing file's directory;
// otherwise the dotted name is repo-relative. Package modules (`pkg/__init__.py`)
// are not resolved, for the same reason directory modules are not above.
func pythonModuleFile(fromFile, module string) string {
	if module == "" {
		return ""
	}
	if !strings.HasPrefix(module, ".") {
		return strings.ReplaceAll(module, ".", "/") + ".py"
	}
	up := 0
	for up < len(module) && module[up] == '.' {
		up++
	}
	dir := path.Dir(fromFile)
	for i := 1; i < up; i++ {
		dir = path.Dir(dir)
	}
	rest := strings.TrimLeft(module, ".")
	if rest == "" {
		return ""
	}
	return path.Join(dir, strings.ReplaceAll(rest, ".", "/")) + ".py"
}
