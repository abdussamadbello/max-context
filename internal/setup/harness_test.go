package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every registered harness must configure a project from scratch without error,
// and must leave AGENTS.md pointing the agent at the tools.
func TestEveryHarnessConfiguresACleanProject(t *testing.T) {
	for _, h := range harnesses {
		t.Run(h.Name, func(t *testing.T) {
			root := t.TempDir()
			r, err := Run(root, h.Name)
			if err != nil {
				t.Fatalf("Run(%s): %v", h.Name, err)
			}
			if len(r.Changes) == 0 {
				t.Fatalf("%s wrote nothing", h.Name)
			}
			agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
			if err != nil {
				t.Fatalf("AGENTS.md: %v", err)
			}
			if !strings.Contains(string(agents), "max-context") {
				t.Errorf("AGENTS.md does not mention max-context:\n%s", agents)
			}
			if h.MCPConfig != "" {
				raw, err := os.ReadFile(filepath.Join(root, h.MCPConfig))
				if err != nil {
					t.Fatalf("expected %s: %v", h.MCPConfig, err)
				}
				var doc map[string]interface{}
				if err := json.Unmarshal(raw, &doc); err != nil {
					t.Fatalf("%s is not valid JSON: %v", h.MCPConfig, err)
				}
				servers, ok := doc[h.serversKey()].(map[string]interface{})
				if !ok {
					t.Fatalf("%s has no %q object:\n%s", h.MCPConfig, h.serversKey(), raw)
				}
				if _, ok := servers[serverName]; !ok {
					t.Errorf("%s did not register max-context under %q", h.Name, h.serversKey())
				}
			}
			if h.GuidancePath != "" {
				if _, err := os.Stat(filepath.Join(root, h.GuidancePath)); err != nil {
					t.Errorf("guidance file missing: %v", err)
				}
			}
		})
	}
}

// Re-running setup must change nothing, so it is safe to run on every checkout.
func TestEveryHarnessIsIdempotent(t *testing.T) {
	for _, h := range harnesses {
		t.Run(h.Name, func(t *testing.T) {
			root := t.TempDir()
			if _, err := Run(root, h.Name); err != nil {
				t.Fatal(err)
			}
			before := snapshotTree(t, root)

			r, err := Run(root, h.Name)
			if err != nil {
				t.Fatal(err)
			}
			after := snapshotTree(t, root)

			if len(before) != len(after) {
				t.Fatalf("second run changed the file set: %d -> %d", len(before), len(after))
			}
			for path, content := range before {
				if after[path] != content {
					t.Errorf("second run rewrote %s", path)
				}
			}
			for _, c := range r.Changes {
				if c.Action == ActionCreated || c.Action == ActionUpdated {
					t.Errorf("second run reported %s on %s; expected everything unchanged", c.Action, c.Path)
				}
			}
		})
	}
}

// `setup all` must apply every harness, and stay idempotent across them — they
// share AGENTS.md and .gitignore.
func TestSetupAllAppliesEveryHarness(t *testing.T) {
	root := t.TempDir()
	if _, err := Run(root, "all"); err != nil {
		t.Fatalf("Run(all): %v", err)
	}
	for _, h := range harnesses {
		if h.GuidancePath == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, h.GuidancePath)); err != nil {
			t.Errorf("setup all skipped %s: %v", h.Name, err)
		}
	}
	// AGENTS.md must carry exactly one max-context block, not one per harness.
	agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(agents), MarkerStart); n != 1 {
		t.Errorf("AGENTS.md has %d max-context blocks, want 1:\n%s", n, agents)
	}
}

// An unknown target must name the real ones rather than failing blankly.
func TestUnknownTargetListsKnownHarnesses(t *testing.T) {
	_, err := Run(t.TempDir(), "not-a-harness")
	if err == nil {
		t.Fatal("expected an error for an unknown target")
	}
	for _, name := range HarnessNames() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not mention %q: %v", name, err)
		}
	}
}

// The registry is the contract: a harness that declares a command style needs
// somewhere to put the files, and every name must be unique.
func TestHarnessRegistryIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, h := range harnesses {
		if h.Name == "" {
			t.Error("a harness has no name")
		}
		if h.Name == "all" {
			t.Error(`"all" is reserved for applying every harness`)
		}
		if seen[h.Name] {
			t.Errorf("duplicate harness name %q", h.Name)
		}
		seen[h.Name] = true

		needsDir := h.Commands == CommandsFrontmatter ||
			h.Commands == CommandsVSCodeStyle ||
			h.Commands == CommandsWorkflow
		if needsDir && h.CommandsDir == "" {
			t.Errorf("%s declares a command style but no CommandsDir", h.Name)
		}
		if !needsDir && h.CommandsDir != "" {
			t.Errorf("%s sets CommandsDir but writes no command files", h.Name)
		}
		if h.MCPConfig == "" && h.ServersKey != "" {
			t.Errorf("%s sets ServersKey but has no MCPConfig", h.Name)
		}
	}
}

// The property the registry exists for: adding a harness is data, not code.
// This registers one on the fly and asserts it is configured end to end without
// touching any switch statement or writing a new file.
func TestNewHarnessNeedsOnlyATableEntry(t *testing.T) {
	original := harnesses
	t.Cleanup(func() { harnesses = original })

	harnesses = append(append([]Harness{}, original...), Harness{
		Name:         "example-harness",
		MCPConfig:    filepath.Join(".example", "config.json"),
		ServersKey:   "servers", // a harness that uses a different key
		GuidancePath: filepath.Join(".example", "rules", "max-context.md"),
		Guidance:     defaultGuidance,
		Commands:     CommandsFrontmatter,
		CommandsDir:  filepath.Join(".example", "commands"),
		AgentsLine:   defaultAgentsLine,
	})

	root := t.TempDir()
	if _, err := Run(root, "example-harness"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(root, ".example", "config.json"))
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	servers, ok := doc["servers"].(map[string]interface{})
	if !ok {
		t.Fatalf(`custom ServersKey "servers" was not honoured:\n%s`, raw)
	}
	if _, ok := servers[serverName]; !ok {
		t.Errorf("max-context not registered:\n%s", raw)
	}
	for _, c := range Commands {
		if _, err := os.Stat(filepath.Join(root, ".example", "commands", c.Name+".md")); err != nil {
			t.Errorf("command %s not written: %v", c.Name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".example", "rules", "max-context.md")); err != nil {
		t.Errorf("guidance not written: %v", err)
	}
}

// snapshotTree reads every file under root into a map for change detection.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
