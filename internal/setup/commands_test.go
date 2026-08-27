package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandsCatalog(t *testing.T) {
	if len(Commands) != 4 {
		t.Fatalf("expected 4 commands, got %d", len(Commands))
	}
	names := map[string]string{}
	for _, c := range Commands {
		names[c.Name] = c.Shell
	}
	for name, shell := range map[string]string{
		"reindex": "max-context --reindex",
		"index":   "max-context --index",
		"status":  "max-context --status",
		"context": `max-context context --task "<the task>" --budget 4000`,
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
	if err := applyHarness(t, "claude-code", root, nil); err != nil {
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

func TestSetupVSCodeWritesPrompts(t *testing.T) {
	root := t.TempDir()
	if err := applyHarness(t, "vscode", root, nil); err != nil {
		t.Fatalf("setupVSCode: %v", err)
	}
	for file, shell := range map[string]string{
		"reindex.prompt.md": "max-context --reindex",
		"index.prompt.md":   "max-context --index",
		"status.prompt.md":  "max-context --status",
	} {
		p := filepath.Join(root, ".github", "prompts", file)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("expected %s: %v", p, err)
		}
		s := string(data)
		if !strings.Contains(s, shell) || !strings.Contains(s, "description:") {
			t.Errorf("%s missing shell %q or description frontmatter", file, shell)
		}
	}
}

func TestSetupCursorWritesCommands(t *testing.T) {
	root := t.TempDir()
	if err := applyHarness(t, "cursor", root, nil); err != nil {
		t.Fatalf("setupCursor: %v", err)
	}
	for file, shell := range map[string]string{
		"reindex.md": "max-context --reindex",
		"index.md":   "max-context --index",
		"status.md":  "max-context --status",
	} {
		p := filepath.Join(root, ".cursor", "commands", file)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("expected %s: %v", p, err)
		}
		if !strings.Contains(string(data), shell) {
			t.Errorf("%s missing %q", file, shell)
		}
	}
}

func TestSetupWindsurfWritesWorkflows(t *testing.T) {
	root := t.TempDir()
	if err := applyHarness(t, "windsurf", root, nil); err != nil {
		t.Fatalf("setupWindsurf: %v", err)
	}
	for file, shell := range map[string]string{
		"reindex.md": "max-context --reindex",
		"index.md":   "max-context --index",
		"status.md":  "max-context --status",
	} {
		p := filepath.Join(root, ".windsurf", "workflows", file)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("expected %s: %v", p, err)
		}
		if !strings.Contains(string(data), shell) {
			t.Errorf("%s missing %q", file, shell)
		}
	}
}

func TestRenderSkillCommandsSection(t *testing.T) {
	out := renderSkillCommandsSection(Commands)
	for _, want := range []string{
		"## Commands",
		"### reindex",
		"max-context --reindex",
		"max-context --index",
		"max-context --status",
		// Body guidance must reach skill-doc IDEs too (parity with file-based IDEs).
		".max-context/.reindex-queue",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("section missing %q", want)
		}
	}
}

func TestSetupCodexSkillHasCommands(t *testing.T) {
	root := t.TempDir()
	if err := applyHarness(t, "codex", root, nil); err != nil {
		t.Fatalf("setupCodex: %v", err)
	}
	p := filepath.Join(root, ".codex", "skills", "max-context", "SKILL.md")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("expected %s: %v", p, err)
	}
	if !strings.Contains(string(data), "max-context --reindex") {
		t.Errorf("codex SKILL.md missing commands section")
	}
}

func TestSetupAntigravitySkillHasCommands(t *testing.T) {
	root := t.TempDir()
	if err := applyHarness(t, "antigravity", root, nil); err != nil {
		t.Fatalf("setupAntigravity: %v", err)
	}
	p := filepath.Join(root, ".agent", "skills", "max-context", "SKILL.md")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("expected %s: %v", p, err)
	}
	if !strings.Contains(string(data), "max-context --status") {
		t.Errorf("antigravity SKILL.md missing commands section")
	}
}

func TestSetupCommandsAreIdempotent(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 2; i++ {
		if err := applyHarness(t, "claude-code", root, nil); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	// A user edit must survive a second setup run.
	p := filepath.Join(root, ".claude", "commands", "reindex.md")
	if err := os.WriteFile(p, []byte("EDITED"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := applyHarness(t, "claude-code", root, nil); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "EDITED" {
		t.Errorf("user edit was overwritten: %q", string(data))
	}
}
