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

// Lexical scope chain.
//
// enclosing returns the INNERMOST span containing a line, which is right for
// asking "which function is this call in" and wrong for asking "what is this
// identifier's type". A nested function sees the bindings of the function it is
// written inside; looking only at the innermost span loses every one of them.
//
// The bug was invisible because it is grammar-shaped. Python's `def inner()`
// produces a span, so a closure over a typed parameter resolved nothing, while
// the same code in Go — where the nested form is an anonymous literal and no
// span is created — resolved fine. TypeScript failed with a nested `function`
// declaration and succeeded with a function expression. Three languages, three
// different-looking symptoms, one missing rule.

// enclosingChain returns every span containing line, innermost first, so a
// lookup can walk outward the way lexical scoping does.
func enclosingChain(spans []*funcSpan, line int) []*funcSpan {
	var chain []*funcSpan
	for _, s := range spans {
		if line >= s.start && line <= s.end {
			chain = append(chain, s)
		}
	}
	// Innermost first: a span nested inside another has a later start and an
	// earlier end, so ordering by width puts the tightest scope at the front.
	for i := 1; i < len(chain); i++ {
		for j := i; j > 0 && spanWidth(chain[j]) < spanWidth(chain[j-1]); j-- {
			chain[j], chain[j-1] = chain[j-1], chain[j]
		}
	}
	return chain
}

func spanWidth(s *funcSpan) int { return s.end - s.start }

// lookupLocalType resolves an identifier's type from the nearest scope that
// binds it, shadowing correctly: an inner binding wins over an outer one.
func lookupLocalType(spans []*funcSpan, line int, name string) (string, bool) {
	for _, s := range enclosingChain(spans, line) {
		if typ, ok := s.types[name]; ok {
			return typ, true
		}
	}
	return "", false
}

// lookupFromCallee resolves an identifier bound by `x := f()` from the nearest
// scope that binds it.
func lookupFromCallee(spans []*funcSpan, line int, name string) (string, bool) {
	for _, s := range enclosingChain(spans, line) {
		if callee, ok := s.fromCallee[name]; ok {
			return callee, true
		}
	}
	return "", false
}
