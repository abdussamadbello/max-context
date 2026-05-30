package setup

import "testing"

func TestCommandsCatalog(t *testing.T) {
	if len(Commands) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(Commands))
	}
	names := map[string]string{}
	for _, c := range Commands {
		names[c.Name] = c.Shell
	}
	for name, shell := range map[string]string{
		"reindex": "max-context --reindex",
		"index":   "max-context --index",
		"status":  "max-context --status",
	} {
		if names[name] != shell {
			t.Errorf("command %q: want shell %q, got %q", name, shell, names[name])
		}
	}
}

func TestRenderFrontmatterCommand(t *testing.T) {
	out := renderFrontmatterCommand(reindexCmd)
	for _, want := range []string{
		"---",
		"name: reindex",
		"description: Rebuild the max-context index.",
		"max-context --reindex",
	} {
		if !contains(out, want) {
			t.Errorf("rendered command missing %q\n---\n%s", want, out)
		}
	}
}

// contains is a tiny helper to avoid importing strings in every assertion.
func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		indexOf(haystack, needle) >= 0)
}
func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
