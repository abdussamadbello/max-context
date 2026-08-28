package indexer

import (
	"strings"

	"github.com/maxcontext/max-context/pkg/treesitter"
)

// Per-language conventions for the parts of resolution that genuinely differ
// between languages, expressed as data.
//
// The rest of the resolver is language-agnostic, but interface satisfaction was
// not: it recognised an interface by `definition LIKE 'interface%'`, which is Go
// syntax, so no other language got the relation at all. TypeScript is indexed
// deeply — 130 lines of queries and a dedicated parser — and still had zero
// implementations recorded, because its interface bodies start with `{`. That
// divergence is invisible in the output: every language reports the same
// confidence vocabulary whether or not the analysis behind it ran.
//
// Adding a language here is a table entry, not a new code path, which is the
// same shape the setup harness registry already uses.

// SatisfactionRule is how a language decides that a concrete type satisfies an
// interface.
type SatisfactionRule int

const (
	// SatisfactionStructural infers satisfaction by comparing method sets: a type
	// satisfies an interface when its methods cover the interface's. Go works
	// this way and the language offers nothing better, so the inference is
	// unavoidable — and imprecise, since it matches on name alone.
	SatisfactionStructural SatisfactionRule = iota
	// SatisfactionDeclared reads satisfaction off an explicit clause the source
	// already states (`class C implements I`). Exact: it produces no false
	// positives, because the author said so.
	SatisfactionDeclared
)

// LanguageConvention describes one language's interface handling.
type LanguageConvention struct {
	// InterfaceKinds are the values of types.kind that denote an implementable
	// interface for this language. A type alias is not one, even where the
	// grammar produces a similar node.
	InterfaceKinds []string

	// DefinitionPrefix, when set, additionally requires the type's definition
	// text to start with it. Go reuses kind "type" for every named type, so the
	// `interface` keyword is what separates an interface from a struct.
	DefinitionPrefix string

	// Satisfaction is how this language's implementations are established.
	Satisfaction SatisfactionRule
}

// languageConventions is the registry. Languages absent from it get no
// interface satisfaction, which is the honest state for a language whose
// conventions nobody has written down — as opposed to silently running Go's
// rule against it.
var languageConventions = map[string]LanguageConvention{
	"go": {
		InterfaceKinds:   []string{"type", "interface"},
		DefinitionPrefix: "interface",
		Satisfaction:     SatisfactionStructural,
	},
	"typescript": {
		InterfaceKinds: []string{"interface"},
		Satisfaction:   SatisfactionDeclared,
	},
	"tsx": {
		InterfaceKinds: []string{"interface"},
		Satisfaction:   SatisfactionDeclared,
	},
}

// isInterfaceType reports whether a types row is an implementable interface
// under some language's conventions. Language is taken from the row where
// available; when it is empty the definition prefix still discriminates, which
// keeps Go working on rows indexed before language was recorded on types.
func isInterfaceType(language, kind, definition string) bool {
	kind = strings.ToLower(strings.TrimSpace(kind))
	def := strings.TrimSpace(definition)
	if c, ok := languageConventions[strings.ToLower(strings.TrimSpace(language))]; ok {
		return matchesConvention(c, kind, def)
	}
	// Unknown or unrecorded language: accept only an unambiguous syntactic
	// marker, never a bare kind, so a TypeScript type alias is not mistaken for
	// an interface by a Go rule.
	for _, c := range languageConventions {
		if c.DefinitionPrefix != "" && matchesConvention(c, kind, def) {
			return true
		}
	}
	return false
}

func matchesConvention(c LanguageConvention, kind, def string) bool {
	kindOK := false
	for _, k := range c.InterfaceKinds {
		if k == kind {
			kindOK = true
			break
		}
	}
	if !kindOK {
		return false
	}
	if c.DefinitionPrefix != "" {
		return strings.HasPrefix(def, c.DefinitionPrefix)
	}
	return true
}

// satisfactionRuleFor reports how a language establishes satisfaction, and
// whether it has a convention at all.
func satisfactionRuleFor(language string) (SatisfactionRule, bool) {
	c, ok := languageConventions[strings.ToLower(strings.TrimSpace(language))]
	if !ok {
		return SatisfactionStructural, false
	}
	return c.Satisfaction, true
}

// languageOfPath names the language a file is written in, for looking up its
// conventions. Derived from the path rather than stored on the row: the types
// table has no language column, and the extension is authoritative anyway.
func languageOfPath(path string) string {
	lang, ok := treesitter.LanguageForPath(path)
	if !ok {
		return ""
	}
	return string(lang)
}
