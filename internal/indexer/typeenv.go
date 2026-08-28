package indexer

// The local type environment, shared across languages.
//
// Each language parser declared its own copies of these record types *inside*
// the parse function, so the logic that consumes them had to be written per
// language too. It was, twice, identically — once for Go's `for _, n := range
// ns` and once for TypeScript's `for (const n of ns)` — and Python got neither,
// because nothing links the three implementations and nothing reports that one
// of them is missing a rule the others have.
//
// The shapes below are package-level so the logic can be written once. What
// stays per language is which grammar node produces which record, which is a
// query concern and genuinely differs; how a binding takes its type does not.

// typedIdent binds an identifier to a type name at a source line.
type typedIdent struct {
	name, typ string
	line      int
}

// derivedBinding binds an identifier to the ELEMENT type of another identifier:
// `for _, n := range ns`, `for (const n of ns)`, `for n in ns`, `n := ns[0]`.
type derivedBinding struct {
	name, src string
	line      int
}

// bindElementTypes types every derived binding from its source's element type,
// writing results into the enclosing span's type map.
//
// Element types are deliberately NOT written to span.types themselves: `ns
// []Notifier` means ns is a slice, not a Notifier, and typing ns as one would
// invent a method call the code never makes. A binding whose source has no
// recorded element type stays untyped rather than guessing, so an unresolvable
// loop produces no edge instead of a wrong one.
func bindElementTypes(spans []*funcSpan, elems []typedIdent, bindings []derivedBinding) {
	if len(elems) == 0 || len(bindings) == 0 {
		return
	}
	byScope := map[*funcSpan]map[string]string{}
	for _, e := range elems {
		s := enclosing(spans, e.line)
		if s == nil {
			continue
		}
		if byScope[s] == nil {
			byScope[s] = map[string]string{}
		}
		byScope[s][e.name] = e.typ
	}
	for _, b := range bindings {
		s := enclosing(spans, b.line)
		if s == nil {
			continue
		}
		if typ, ok := byScope[s][b.src]; ok {
			s.types[b.name] = typ
		}
	}
}
