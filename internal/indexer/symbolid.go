package indexer

import "strings"

// Symbol IDs, in the shape of SCIP's symbol strings.
//
// Every tool here addresses code by bare name, which cannot express the
// question a caller usually means. A repository with `EmailNotifier.Send`,
// `SMSNotifier.Send`, and an unrelated `MetricsBuffer.Send` offers
// get_call_chain no way to ask about one of them: the three collapse into
// "Send", and the answer mixes them. That ambiguity is what made the dispatch
// probe's precision cap out at 0.67 for every arm, max-context included
// (experiments/eval/benchmarks/in-house/DISPATCH.md).
//
// The grammar is SCIP's (https://github.com/scip-code/scip/blob/main/docs/scip.md):
//
//	<symbol>    ::= <scheme> ' ' <package> ' ' (<descriptor>)+
//	<package>   ::= <manager> ' ' <package-name> ' ' <version>
//	<namespace> ::= <name> '/'
//	<type>      ::= <name> '#'
//	<term>      ::= <name> '.'
//	<method>    ::= <name> '(' <disambiguator>? ').'
//
// with '.' as the placeholder for an unknown component. Following it rather
// than inventing a format costs nothing and keeps SCIP tooling readable against
// this index later.
//
// What this is NOT: a SCIP index. There is no dependency resolution, so manager
// and version are always placeholders and a symbol is unique within one indexed
// repository, not across repositories. Claiming otherwise would be the kind of
// number this project withdraws.
const symbolPlaceholder = "."

// SymbolID builds the symbol string for one definition.
//
// receiverType is the enclosing type for a method and empty for a free
// function; kind distinguishes the descriptor suffix. An empty name yields an
// empty symbol rather than a syntactically valid symbol naming nothing.
func SymbolID(language, pkg, receiverType, kind, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(placeholderOr(language))
	b.WriteString(" ")
	// <package> is three components; only the name is knowable from a local index.
	b.WriteString(symbolPlaceholder + " " + placeholderOr(pkg) + " " + symbolPlaceholder)
	b.WriteString(" ")
	if rt := strings.TrimSpace(receiverType); rt != "" {
		b.WriteString(escapeSymbolName(rt))
		b.WriteString("#")
	}
	b.WriteString(escapeSymbolName(name))
	b.WriteString(descriptorSuffix(kind))
	return b.String()
}

// descriptorSuffix maps an indexed kind to its SCIP descriptor suffix. Kinds
// this indexer does not distinguish fall back to the method suffix, which is
// what the overwhelming majority of indexed definitions are.
func descriptorSuffix(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "type", "class", "struct", "interface":
		return "#"
	case "const", "var", "field", "property":
		return "."
	default: // func, method, and anything unrecognised
		return "()."
	}
}

func placeholderOr(s string) string {
	if s = strings.TrimSpace(s); s != "" {
		return escapeSymbolName(s)
	}
	return symbolPlaceholder
}

// escapeSymbolName escapes the one character the grammar reserves inside a
// name: a space is written as two spaces. Without this a package or type
// containing a space would silently shift every following component.
func escapeSymbolName(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), " ", "  ")
}
