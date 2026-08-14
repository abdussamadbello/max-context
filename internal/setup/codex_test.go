package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func readCodexConfig(t *testing.T, root string) map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf(".codex/config.toml: %v", err)
	}
	var doc map[string]interface{}
	if _, err := toml.Decode(string(raw), &doc); err != nil {
		t.Fatalf("not valid TOML: %v\n%s", err, raw)
	}
	return doc
}

// Codex setup used to write only a skill file: the MCP server was never
// registered anywhere, so the tools never appeared and setup still reported
// success. This is the same silent no-op the other harnesses had.
func TestCodexRegistersMCPServer(t *testing.T) {
	root := t.TempDir()
	if _, err := Run(root, "codex"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	doc := readCodexConfig(t, root)
	servers, ok := doc["mcp_servers"].(map[string]interface{})
	if !ok {
		t.Fatalf("no [mcp_servers] table: %#v", doc)
	}
	entry, ok := servers[serverName].(map[string]interface{})
	if !ok {
		t.Fatalf("max-context not registered: %#v", servers)
	}
	if entry["command"] != serverName {
		t.Errorf("command = %v, want %q", entry["command"], serverName)
	}
}

// Codex only loads project config for trusted projects, so a config written
// here can be entirely inert. Setup must say so rather than implying success.
func TestCodexReportsTheTrustRequirement(t *testing.T) {
	root := t.TempDir()
	r, err := Run(root, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Notes) == 0 {
		t.Fatal("no note about the trust requirement")
	}
	joined := strings.Join(r.Notes, "\n")
	for _, want := range []string{"trust", "config.toml"} {
		if !strings.Contains(strings.ToLower(joined), strings.ToLower(want)) {
			t.Errorf("note does not mention %q:\n%s", want, joined)
		}
	}
}

// TOML tables can be declared in any order, so max-context appends its table
// rather than re-serialising. That must leave every existing byte intact —
// comments, ordering, and formatting included.
func TestCodexAppendPreservesFileExactly(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".codex")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	existing := `# my codex config
model = "gpt-5-codex"

# keep this server
[mcp_servers.context7]
command = "npx"
args = ["-y", "@upstash/context7-mcp"]

[mcp_servers.context7.env]
MY_ENV_VAR = "MY_ENV_VALUE"
`
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(root, "codex"); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(path)
	body := string(raw)
	if !strings.HasPrefix(body, existing) {
		t.Errorf("existing content was not preserved byte for byte:\n%s", body)
	}
	for _, want := range []string{"# my codex config", "# keep this server", `MY_ENV_VAR = "MY_ENV_VALUE"`} {
		if !strings.Contains(body, want) {
			t.Errorf("lost %q from the config:\n%s", want, body)
		}
	}

	doc := readCodexConfig(t, root)
	if doc["model"] != "gpt-5-codex" {
		t.Errorf("dropped an unrelated key:\n%s", body)
	}
	servers, _ := doc["mcp_servers"].(map[string]interface{})
	if _, ok := servers["context7"]; !ok {
		t.Errorf("dropped the user's existing server:\n%s", body)
	}
	if _, ok := servers[serverName]; !ok {
		t.Errorf("did not add max-context:\n%s", body)
	}
}

func TestCodexMergeIsIdempotent(t *testing.T) {
	root := t.TempDir()
	if _, err := Run(root, "codex"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".codex", "config.toml")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Run(root, "codex")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Errorf("second run rewrote the config:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	for _, c := range r.Changes {
		if c.Action == ActionCreated || c.Action == ActionUpdated {
			t.Errorf("second run reported %s on %s", c.Action, c.Path)
		}
	}
}

// An entry written in quoted-key form is still an existing entry.
func TestCodexDetectsQuotedKeyForm(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".codex")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	existing := "[mcp_servers.\"max-context\"]\ncommand = \"max-context\"\nargs = []\n"
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(root, "codex"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != existing {
		t.Errorf("re-registered a server that was already there in quoted form:\n%s", raw)
	}
}

// A config we cannot parse must never be clobbered.
func TestCodexRefusesUnparseableTOML(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".codex")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	broken := "[mcp_servers\ncommand = \"oops\n"
	if err := os.WriteFile(path, []byte(broken), 0644); err != nil {
		t.Fatal(err)
	}
	r, err := Run(root, "codex")
	if err != nil {
		t.Fatalf("a broken config must not fail the whole setup: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != broken {
		t.Errorf("clobbered an unparseable config:\n%s", after)
	}
	skipped := r.Skipped()
	if len(skipped) == 0 {
		t.Fatalf("expected the file to be skipped, got %+v", r.Changes)
	}
	if !strings.Contains(skipped[0].Note, "mcp_servers") {
		t.Errorf("skip note should show what to append, got %q", skipped[0].Note)
	}
}

// The generated table must be valid TOML that round-trips to what we intended.
func TestRenderTOMLSectionIsValid(t *testing.T) {
	for _, entry := range []map[string]interface{}{
		{"command": "max-context", "args": []interface{}{}},
		{"command": "max-context", "args": []interface{}{"-project", "/some/path"}},
		{"command": "max-context", "args": []interface{}{}, "enabled": true},
	} {
		section := renderTOMLSection("mcp_servers", entry)
		var doc map[string]interface{}
		if _, err := toml.Decode(section, &doc); err != nil {
			t.Fatalf("generated invalid TOML: %v\n%s", err, section)
		}
		servers, ok := doc["mcp_servers"].(map[string]interface{})
		if !ok {
			t.Fatalf("no mcp_servers table in:\n%s", section)
		}
		got, ok := servers[serverName].(map[string]interface{})
		if !ok {
			t.Fatalf("no max-context entry in:\n%s", section)
		}
		if got["command"] != entry["command"] {
			t.Errorf("command round-tripped as %v, want %v", got["command"], entry["command"])
		}
	}
}

func TestTOMLKeyQuoting(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"max-context", "max-context"}, // hyphens are legal in bare keys
		{"simple", "simple"},
		{"with_underscore", "with_underscore"},
		{"has.dot", `"has.dot"`},
		{"has space", `"has space"`},
		{"", `""`},
	} {
		if got := tomlKey(tc.in); got != tc.want {
			t.Errorf("tomlKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
