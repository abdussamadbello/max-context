package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		if !strings.Contains(out, want) {
			t.Errorf("rendered command missing %q\n---\n%s", want, out)
		}
	}
}

func TestSetupClaudeCodeWritesCommands(t *testing.T) {
	root := t.TempDir()
	if err := setupClaudeCode(root); err != nil {
		t.Fatalf("setupClaudeCode: %v", err)
	}
	for file, shell := range map[string]string{
		"reindex.md": "max-context --reindex",
		"index.md":   "max-context --index",
		"status.md":  "max-context --status",
	} {
		p := filepath.Join(root, ".claude", "commands", file)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("expected %s: %v", p, err)
		}
		if !strings.Contains(string(data), shell) {
			t.Errorf("%s missing %q", file, shell)
		}
	}
}
