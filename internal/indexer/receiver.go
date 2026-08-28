package indexer

// Receiver classification, shared across languages.
//
// Deciding what a call's receiver is — an imported module alias, a statically
// typed local, a local bound by another call, or a package/module global — was
// written out separately in parseGo, parseTS and parsePython. The three copies
// were identical apart from one extra field Go records for imports, which is
// the tell: the decision is about scope, and scope does not vary by language
// the way grammar does.
//
// Three copies is three places for a rule to drift, and drift here is invisible
// in the output: every language reports the same resolution vocabulary whether
// or not it applied the same rules to earn it.

// classifyReceiver fills a call's receiver fields from the scope at its line,
// and reports the import path when the receiver is an imported alias so a
// language that records more about imports can add it.
//
// The order is deliberate and is the precedence a reader would expect: an
// imported alias shadows everything, a declared local type beats an inferred
// one, and "global" is what is left when no local binding explains the name —
// a guess the resolver is told to treat as such, not a claim.
func (cr *CallRecord) classifyReceiver(recv string, imports map[string]string, spans []*funcSpan, line int) (importPath string) {
	if recv == "" {
		return ""
	}
	if path := imports[recv]; path != "" {
		cr.ReceiverKind = "import"
		return path
	}
	if s := enclosing(spans, line); s != nil {
		if typ, ok := s.types[recv]; ok {
			cr.ReceiverKind = "var"
			cr.ReceiverType = typ
			return ""
		}
		if callee, ok := s.fromCallee[recv]; ok {
			// x := callee(); x.M() — the resolver types x from callee's return.
			cr.ReceiverKind = "from-callee"
			cr.ReceiverFromCallee = callee
			return ""
		}
	}
	cr.ReceiverKind = "maybe-global"
	return ""
}

// linkBareImportedCall records the original symbol a bare call's name was
// imported under, so the resolver can link it cross-file even when the local
// name is an alias. A call with a receiver is not a bare call and is left
// alone.
func (cr *CallRecord) linkBareImportedCall(callee string, importedSymbols map[string]importedSym) {
	if cr.ReceiverName != "" {
		return
	}
	if sym, ok := importedSymbols[callee]; ok {
		cr.ImportedOrigin = sym.origin
		cr.ImportedModuleRoot = sym.root
	}
}

// classifyFieldReceiver fills the receiver fields for a `base.field.method()`
// call, where the method belongs to the type of a field rather than of the base.
//
// The three parsers each implemented one half of this. Go typed the base from
// the local scope but knew nothing about self/this, having no classes.
// TypeScript and Python assumed the base WAS self/this and typed the field from
// the enclosing class — Python while capturing the real base identifier and
// discarding it. So `h.db.query()` inside a method recorded a receiver of
// `self.db` and resolved against the enclosing class's field, at receiver-typed
// confidence. Where the two classes' fields had different types that is a
// confidently wrong answer, not a missing one.
//
// isSelf reports whether the base is the language's self/this keyword; pass nil
// for a language without one.
func (cr *CallRecord) classifyFieldReceiver(
	base, field string,
	isSelf func(string) bool,
	spans []*funcSpan, clsSpans []*classSpan, line int,
) {
	cr.ReceiverName = base + "." + field
	cr.ReceiverField = field

	if isSelf != nil && isSelf(base) {
		// The field belongs to the enclosing class; there is no local binding
		// for self/this to look up.
		if cs := enclosingClass(clsSpans, line); cs != nil {
			cr.ReceiverKind = "field"
			cr.ReceiverType = cs.name
			return
		}
		cr.ReceiverKind = "unresolved-field"
		return
	}

	// An ordinary base: its own type must be known before the field's can be.
	if s := enclosing(spans, line); s != nil {
		if typ, ok := s.types[base]; ok {
			cr.ReceiverKind = "field"
			cr.ReceiverType = typ // the BASE's type; ReceiverField names the field
			return
		}
	}
	cr.ReceiverKind = "unresolved-field"
}

// selfKeyword returns an isSelf predicate for one keyword, or nil when the
// language has none.
func selfKeyword(word string) func(string) bool {
	if word == "" {
		return nil
	}
	return func(s string) bool { return s == word }
}
