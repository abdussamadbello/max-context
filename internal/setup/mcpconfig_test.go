package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readServers returns the mcpServers object from an IDE config file.
func readServers(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, raw)
	}
	servers, ok := doc["mcpServers"].(map[string]interface{})
	if !ok {
		t.Fatalf("no mcpServers object in %s:\n%s", path, raw)
	}
	return servers
}

// The regression this whole file exists for: setup used to write the MCP config
// only when the file was absent, so every user who already had one — i.e. anyone
// already using MCP — got a successful-looking setup that never registered
// max-context. The tools then simply never appeared in their editor.
func TestSetupRegistersAlongsideExistingServers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		relPath string
		run     func(root string, r *Report) error
	}{
		{"cursor", filepath.Join(".cursor", "mcp.json"), setupCursor},
		{"claude-code", ".mcp.json", setupClaudeCode},
		{"vscode", filepath.Join(".vscode", "mcp.json"), setupVSCode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, tc.relPath)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			existing := `{
  "mcpServers": {
    "my-server": {"command": "keep-me", "args": ["--flag"]}
  },
  "someOtherKey": {"preserved": true}
}`
			if err := os.WriteFile(path, []byte(existing), 0644); err != nil {
				t.Fatal(err)
			}

			if err := tc.run(root, nil); err != nil {
				t.Fatalf("setup: %v", err)
			}

			servers := readServers(t, path)
			if _, ok := servers[serverName]; !ok {
				t.Errorf("max-context was not registered in %s", tc.relPath)
			}
			if _, ok := servers["my-server"]; !ok {
				t.Errorf("pre-existing server was dropped from %s", tc.relPath)
			}

			raw, _ := os.ReadFile(path)
			var doc map[string]interface{}
			_ = json.Unmarshal(raw, &doc)
			if _, ok := doc["someOtherKey"]; !ok {
				t.Errorf("unrelated top-level key was dropped from %s", tc.relPath)
			}
		})
	}
}

func TestMergeMCPConfigCreatesWhenAbsent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".cursor", "mcp.json")
	if err := mergeMCPConfig(path, "mcpServers", nil); err != nil {
		t.Fatalf("mergeMCPConfig: %v", err)
	}
	if _, ok := readServers(t, path)[serverName]; !ok {
		t.Error("max-context missing from freshly created config")
	}
}

func TestMergeMCPConfigIsIdempotent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mcp.json")
	if err := mergeMCPConfig(path, "mcpServers", nil); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := NewReport(root)
	if err := mergeMCPConfig(path, "mcpServers", r); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("second run rewrote the file:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if len(r.Changes) != 1 || r.Changes[0].Action != ActionUnchanged {
		t.Errorf("expected one 'unchanged' change, got %+v", r.Changes)
	}
}

// A user's hand-written config (or JSONC with comments) must never be clobbered
// by an automatic edit. Refuse, and tell them what to paste.
func TestMergeMCPConfigRefusesUnparseableFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mcp.json")
	original := "{\n  // a comment JSON cannot parse\n  \"mcpServers\": {}\n}"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	r := NewReport(root)
	if err := mergeMCPConfig(path, "mcpServers", r); err != nil {
		t.Fatalf("should not fail the whole setup: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != original {
		t.Errorf("clobbered an unparseable config:\n%s", after)
	}
	skipped := r.Skipped()
	if len(skipped) != 1 {
		t.Fatalf("expected one skipped change, got %+v", r.Changes)
	}
	if !strings.Contains(skipped[0].Note, serverName) {
		t.Errorf("skip note should tell the user what to add, got %q", skipped[0].Note)
	}
}

func TestMergeMCPConfigRefusesNonObjectServersKey(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mcp.json")
	original := `{"mcpServers": ["not", "an", "object"]}`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	r := NewReport(root)
	if err := mergeMCPConfig(path, "mcpServers", r); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != original {
		t.Errorf("overwrote a config whose mcpServers is not an object:\n%s", after)
	}
	if len(r.Skipped()) != 1 {
		t.Errorf("expected the file to be skipped, got %+v", r.Changes)
	}
}

func TestMergeMCPConfigHandlesEmptyFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mcp.json")
	if err := os.WriteFile(path, []byte("   \n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := mergeMCPConfig(path, "mcpServers", nil); err != nil {
		t.Fatalf("mergeMCPConfig on empty file: %v", err)
	}
	if _, ok := readServers(t, path)[serverName]; !ok {
		t.Error("max-context missing after merging into an empty file")
	}
}

// Setup must tell the user what it did; the silent exit-0 was indistinguishable
// from a no-op.
func TestSetupReportsWhatItWrote(t *testing.T) {
	root := t.TempDir()
	r, err := Run(root, "cursor")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(r.Changes) == 0 {
		t.Fatal("report is empty")
	}
	out := r.String()
	if !strings.Contains(out, filepath.Join(".cursor", "mcp.json")) {
		t.Errorf("report does not mention the MCP config:\n%s", out)
	}
	var created int
	for _, c := range r.Changes {
		if c.Action == ActionCreated {
			created++
		}
	}
	if created == 0 {
		t.Errorf("expected some files to be created, got %+v", r.Changes)
	}
}

// Report paths are relative to the project root so the summary stays readable.
func TestReportPathsAreRelative(t *testing.T) {
	root := t.TempDir()
	r, err := Run(root, "cursor")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range r.Changes {
		if filepath.IsAbs(c.Path) {
			t.Errorf("report path is absolute: %s", c.Path)
		}
	}
}
