package indexer

import (
	"strings"
	"testing"
)

func TestSymbolID(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		language, pkg, recv, kind, ident string
		want                             string
	}{
		{
			name:     "method carries its receiver type",
			language: "go", pkg: "notify", recv: "EmailNotifier", kind: "method", ident: "Send",
			want: "go . notify . EmailNotifier#Send().",
		},
		{
			name:     "the same method on another type is a different symbol",
			language: "go", pkg: "notify", recv: "MetricsBuffer", kind: "method", ident: "Send",
			want: "go . notify . MetricsBuffer#Send().",
		},
		{
			name:     "free function has no type descriptor",
			language: "go", pkg: "notify", kind: "func", ident: "DeliverAlert",
			want: "go . notify . DeliverAlert().",
		},
		{
			name:     "type descriptor ends in #",
			language: "go", pkg: "notify", kind: "type", ident: "Notifier",
			want: "go . notify . Notifier#",
		},
		{
			name:     "term descriptor ends in .",
			language: "go", pkg: "notify", kind: "const", ident: "MaxRetries",
			want: "go . notify . MaxRetries.",
		},
		{
			name:     "unknown package is a placeholder, not an empty component",
			language: "python", kind: "func", ident: "main",
			want: "python . . . main().",
		},
		{
			name:     "unrecognised kind falls back to the method suffix",
			language: "ruby", pkg: "app", kind: "wibble", ident: "run",
			want: "ruby . app . run().",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SymbolID(tc.language, tc.pkg, tc.recv, tc.kind, tc.ident); got != tc.want {
				t.Errorf("SymbolID = %q, want %q", got, tc.want)
			}
		})
	}
}

// The whole point is that same-named methods on different types stop colliding.
func TestSymbolIDDisambiguatesSameNamedMethods(t *testing.T) {
	seen := map[string]string{}
	for _, recv := range []string{"EmailNotifier", "SMSNotifier", "MetricsBuffer"} {
		id := SymbolID("go", "dispatch", recv, "method", "Send")
		if prev, dup := seen[id]; dup {
			t.Fatalf("%s and %s produced the same symbol %q", prev, recv, id)
		}
		seen[id] = recv
	}
	if len(seen) != 3 {
		t.Errorf("expected 3 distinct symbols, got %d", len(seen))
	}
}

// A symbol naming nothing is worse than no symbol: it would index and match.
func TestSymbolIDEmptyNameYieldsNoSymbol(t *testing.T) {
	for _, ident := range []string{"", "   "} {
		if got := SymbolID("go", "pkg", "T", "method", ident); got != "" {
			t.Errorf("SymbolID with name %q = %q, want empty", ident, got)
		}
	}
}

// A space inside a component would shift every component after it, so the
// grammar escapes it as two spaces.
func TestSymbolIDEscapesSpaces(t *testing.T) {
	got := SymbolID("go", "my pkg", "", "func", "Run")
	if !strings.Contains(got, "my  pkg") {
		t.Errorf("space in package was not escaped: %q", got)
	}
	// Four single-space separators (scheme, manager, name, version) plus the
	// two spaces the escape itself contributes.
	if n := strings.Count(got, " "); n != 6 {
		t.Errorf("space count = %d, want 6 (4 separators + a 2-space escape): %q", n, got)
	}
	// The descriptor must still be the last component: if the escape leaked, the
	// package would have shifted it.
	if !strings.HasSuffix(got, " Run().") {
		t.Errorf("descriptor is not the final component: %q", got)
	}
}
