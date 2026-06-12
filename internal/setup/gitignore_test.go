package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetupAddsGitignore verifies setup adds .max-context/ to .gitignore
// (creating it if absent), preserves existing content, and is idempotent.
func TestSetupAddsGitignore(t *testing.T) {
	t.Run("creates when absent", func(t *testing.T) {
		root := t.TempDir()
		if err := Run(root, "vscode"); err != nil {
			t.Fatalf("Run: %v", err)
		}
		assertIgnored(t, root)
	})

	t.Run("appends to existing, preserving content, idempotent", func(t *testing.T) {
		root := t.TempDir()
		gi := filepath.Join(root, ".gitignore")
		if err := os.WriteFile(gi, []byte("node_modules/\ndist/\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Run(root, "vscode"); err != nil {
			t.Fatalf("Run: %v", err)
		}
		body := readFile(t, gi)
		if !strings.Contains(body, "node_modules/") || !strings.Contains(body, "dist/") {
			t.Errorf("existing .gitignore content not preserved:\n%s", body)
		}
		assertIgnored(t, root)

		// Run again — must not duplicate the entry.
		if err := Run(root, "claude-code"); err != nil {
			t.Fatalf("Run again: %v", err)
		}
		if n := strings.Count(readFile(t, gi), ".max-context/"); n != 1 {
			t.Errorf(".max-context/ appears %d times, want 1 (not idempotent)", n)
		}
	})

	t.Run("respects existing ignore in alternate form", func(t *testing.T) {
		root := t.TempDir()
		gi := filepath.Join(root, ".gitignore")
		if err := os.WriteFile(gi, []byte(".max-context\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Run(root, "vscode"); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if n := strings.Count(readFile(t, gi), ".max-context"); n != 1 {
			t.Errorf("already-ignored entry was duplicated: %q", readFile(t, gi))
		}
	})
}

func assertIgnored(t *testing.T, root string) {
	t.Helper()
	body := readFile(t, filepath.Join(root, ".gitignore"))
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == ".max-context/" {
			return
		}
	}
	t.Errorf(".gitignore does not ignore .max-context/:\n%s", body)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
