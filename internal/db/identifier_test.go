package db

import "testing"

func TestSplitIdentifier(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// The case that motivated this: "resolver cache" could not reach it.
		{"ResolverCache", "Resolver Cache"},
		{"removeFile", "remove File"},
		{"remove_file", "remove file"},
		{"parseGoFile", "parse Go File"},

		// Acronym runs keep their own word rather than exploding per letter.
		{"HTTPServer", "HTTP Server"},
		{"parseHTTPHeader", "parse HTTP Header"},
		{"ServeHTTP", "Serve HTTP"},

		// Digits start a word.
		{"v2Handler", "v 2 Handler"},
		{"parseUTF8String", "parse UTF 8 String"},

		// Separators of every flavour.
		{"snake_case_name", "snake case name"},
		{"kebab-case-name", "kebab case name"},
		{"dotted.path.name", "dotted path name"},

		// Nothing to add: a single word stays empty so we do not duplicate it.
		{"handler", ""},
		{"HANDLER", ""},
		{"x", ""},
		{"", ""},

		// Leading/trailing separators must not produce empty words.
		{"_private", ""},
		{"__dunder__", ""},
		{"_two_words", "two words"},
	} {
		if got := SplitIdentifier(tc.in); got != tc.want {
			t.Errorf("SplitIdentifier(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The point of the column is that a multi-word query reaches the symbol.
func TestSplitIdentifierProducesQueryableWords(t *testing.T) {
	parts := SplitIdentifier("ResolverCache")
	for _, want := range []string{"Resolver", "Cache"} {
		found := false
		for _, w := range identifierWords(parts) {
			if w == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q does not contain the word %q", parts, want)
		}
	}
}
