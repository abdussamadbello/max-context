package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// opencode keys servers under "mcp" and wants a typed entry whose command is a
// single argv array — not the {"command": ..., "args": [...]} shape every other
// harness uses. Writing the common shape produces a config opencode ignores.
func TestOpenCodeUsesTypedArgvEntry(t *testing.T) {
	root := t.TempDir()
	if _, err := Run(root, "opencode"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(root, "opencode.json"))
	if err != nil {
		t.Fatalf("opencode.json: %v", err)
	}
	var doc struct {
		MCP map[string]struct {
			Type    string   `json:"type"`
			Command []string `json:"command"`
			Enabled bool     `json:"enabled"`
		} `json:"mcp"`
		Instructions []string `json:"instructions"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, raw)
	}

	entry, ok := doc.MCP[serverName]
	if !ok {
		t.Fatalf("max-context not registered under \"mcp\":\n%s", raw)
	}
	if entry.Type != "local" {
		t.Errorf("type = %q, want \"local\"", entry.Type)
	}
	if len(entry.Command) != 1 || entry.Command[0] != serverName {
		t.Errorf("command = %v, want [%q]", entry.Command, serverName)
	}
	if !entry.Enabled {
		t.Error("entry is not enabled")
	}

	// opencode has no AGENTS.md convention: guidance is inert unless listed.
	found := false
	for _, p := range doc.Instructions {
		if strings.Contains(p, "max-context") {
			found = true
		}
	}
	if !found {
		t.Errorf("guidance file not listed under \"instructions\": %v", doc.Instructions)
	}
	if _, err := os.Stat(filepath.Join(root, "AGENTS.md")); err == nil {
		t.Error("wrote AGENTS.md for a harness that does not read it")
	}
}

// opencode config merging must preserve the user's other settings.
func TestOpenCodePreservesExistingConfig(t *testing.T) {
	root := t.TempDir()
	existing := `{
  "$schema": "https://opencode.ai/config.json",
  "model": "anthropic/claude-sonnet-5",
  "mcp": {
    "filesystem": {"type": "local", "command": ["npx", "-y", "server-filesystem"]}
  },
  "instructions": ["./docs/house-style.md"]
}`
	if err := os.WriteFile(filepath.Join(root, "opencode.json"), []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(root, "opencode"); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(filepath.Join(root, "opencode.json"))
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["model"] != "anthropic/claude-sonnet-5" {
		t.Errorf("dropped the user's model setting:\n%s", raw)
	}
	mcp, _ := doc["mcp"].(map[string]interface{})
	if _, ok := mcp["filesystem"]; !ok {
		t.Errorf("dropped the user's existing MCP server:\n%s", raw)
	}
	if _, ok := mcp[serverName]; !ok {
		t.Errorf("did not add max-context:\n%s", raw)
	}
	instructions, _ := doc["instructions"].([]interface{})
	if len(instructions) != 2 || instructions[0] != "./docs/house-style.md" {
		t.Errorf("instructions not appended to: %v", instructions)
	}
}

// Hermes keeps a single global YAML config; there is no per-project one.
func TestHermesWritesGlobalYAMLConfig(t *testing.T) {
	root := t.TempDir()
	home := fakeHome(t)
	if _, err := Run(root, "hermes"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(home, ".hermes", "config.yaml"))
	if err != nil {
		t.Fatalf("~/.hermes/config.yaml: %v", err)
	}
	var doc struct {
		MCPServers map[string]struct {
			Command string   `yaml:"command"`
			Args    []string `yaml:"args"`
		} `yaml:"mcp_servers"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("not valid YAML: %v\n%s", err, raw)
	}
	entry, ok := doc.MCPServers[serverName]
	if !ok {
		t.Fatalf("max-context not registered under mcp_servers:\n%s", raw)
	}
	if entry.Command != serverName {
		t.Errorf("command = %q, want %q", entry.Command, serverName)
	}
	// The project itself must be untouched by the MCP registration.
	if _, err := os.Stat(filepath.Join(root, ".hermes", "config.yaml")); err == nil {
		t.Error("wrote a project-local config for a harness that only has a global one")
	}
}

// A global config is hand-maintained, so merging must keep other servers, other
// top-level keys, and the user's comments.
func TestHermesYAMLMergePreservesEverything(t *testing.T) {
	root := t.TempDir()
	home := fakeHome(t)
	cfgDir := filepath.Join(home, ".hermes")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	existing := `# my hermes config
model: hermes-4

mcp_servers:
  # keep this one
  filesystem:
    command: "npx"
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/me"]
`
	path := filepath.Join(cfgDir, "config.yaml")
	if err := os.WriteFile(path, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(root, "hermes"); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(path)
	text := string(raw)
	if !strings.Contains(text, "# my hermes config") || !strings.Contains(text, "# keep this one") {
		t.Errorf("comments were lost in the merge:\n%s", text)
	}
	var doc map[string]interface{}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("merge produced invalid YAML: %v\n%s", err, text)
	}
	if doc["model"] != "hermes-4" {
		t.Errorf("dropped an unrelated top-level key:\n%s", text)
	}
	servers, _ := doc["mcp_servers"].(map[string]interface{})
	if _, ok := servers["filesystem"]; !ok {
		t.Errorf("dropped the user's existing server:\n%s", text)
	}
	if _, ok := servers[serverName]; !ok {
		t.Errorf("did not add max-context:\n%s", text)
	}
}

func TestHermesYAMLMergeIsIdempotent(t *testing.T) {
	root := t.TempDir()
	home := fakeHome(t)
	if _, err := Run(root, "hermes"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".hermes", "config.yaml")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(root, "hermes"); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Errorf("second run rewrote the config:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// A YAML file we cannot parse must never be clobbered — same rule as JSON.
func TestHermesRefusesUnparseableYAML(t *testing.T) {
	root := t.TempDir()
	home := fakeHome(t)
	cfgDir := filepath.Join(home, ".hermes")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfgDir, "config.yaml")
	broken := "mcp_servers:\n  - this is a list\n\t bad indent: [unclosed\n"
	if err := os.WriteFile(path, []byte(broken), 0644); err != nil {
		t.Fatal(err)
	}
	r, err := Run(root, "hermes")
	if err != nil {
		t.Fatalf("a broken global config must not fail the whole setup: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != broken {
		t.Errorf("clobbered an unparseable config:\n%s", after)
	}
	if len(r.Skipped()) == 0 {
		t.Errorf("expected the file to be reported as skipped, got %+v", r.Changes)
	}
}

// A path outside the project must show as absolute, so `setup all` cannot
// quietly touch a global file while the report reads like a project edit.
func TestGlobalConfigIsReportedWithAnAbsolutePath(t *testing.T) {
	root := t.TempDir()
	home := fakeHome(t)
	r, err := Run(root, "hermes")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range r.Changes {
		if strings.HasPrefix(c.Path, home) {
			found = true
		}
		if strings.HasPrefix(c.Path, "..") {
			t.Errorf("path escapes the project as a relative path, which hides it: %s", c.Path)
		}
	}
	if !found {
		t.Errorf("the global config was not reported by absolute path: %+v", r.Changes)
	}
}

// pi ships no MCP client at all — "No MCP. Build CLI tools with READMEs (see
// Skills)" — so its integration is the skill plus the one-shot CLI, and the
// guidance must document commands rather than MCP tools.
func TestPiIsConfiguredWithoutMCP(t *testing.T) {
	root := t.TempDir()
	if _, err := Run(root, "pi"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	skill, err := os.ReadFile(filepath.Join(root, ".pi", "skills", "max-context", "SKILL.md"))
	if err != nil {
		t.Fatalf("pi skill: %v", err)
	}
	body := string(skill)
	for _, want := range []string{
		"max-context def",
		"max-context query",
		"max-context impact",
		"answer_status",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pi guidance does not mention %q — pi has no MCP, so the CLI is the whole surface:\n%s", want, body)
		}
	}

	// pi reads AGENTS.md, and that block should point at the CLI too.
	agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("AGENTS.md: %v", err)
	}
	if !strings.Contains(string(agents), "max-context query") {
		t.Errorf("AGENTS.md does not point pi at the CLI:\n%s", agents)
	}

	// Nothing MCP-shaped should have been written for pi.
	for _, p := range []string{".mcp.json", "opencode.json", filepath.Join(".pi", "mcp.json")} {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			t.Errorf("wrote %s for a harness with no MCP support", p)
		}
	}
}

// Every harness that declares a config must declare how to serialise it and in
// what shape — a mismatch produces a file the harness silently ignores.
func TestHarnessConfigShapesAreDeclared(t *testing.T) {
	for _, h := range harnesses {
		if h.MCPConfig == "" {
			if h.Format != FormatJSON || h.EntryStyle != EntryCommandArgs || h.HomeRelative {
				t.Errorf("%s has no MCPConfig but sets config options", h.Name)
			}
			if h.InstructionsKey != "" {
				t.Errorf("%s sets InstructionsKey but has no config to write it to", h.Name)
			}
			continue
		}
		if h.Format == FormatYAML && h.InstructionsKey != "" {
			t.Errorf("%s wants an instructions array in YAML, which addInstructionsPath cannot write", h.Name)
		}
	}
}
