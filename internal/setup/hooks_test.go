package setup

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pluginHookScripts is the Claude Code plugin's script directory, relative to
// this package. The plugin ships from the repo root; setup writes its own copy.
const pluginHookScripts = "../../hooks/scripts"

// The plugin and setup install the same context-compiler hook from two copies —
// //go:embed cannot reach across the package boundary to share one file. If they
// drift, Claude Code plugin users and VS Code users get different behaviour from
// the same documented feature, silently.
func TestContextCompilerHookDoesNotDrift(t *testing.T) {
	onDisk, err := os.ReadFile(filepath.Join(pluginHookScripts, "context-compiler.sh"))
	if err != nil {
		t.Fatalf("read plugin copy: %v", err)
	}
	if got, want := contextCompilerHook, normaliseNewlines(string(onDisk)); got != want {
		t.Errorf("embedded hook differs from %s/context-compiler.sh; update both", pluginHookScripts)
	}
}

// The compiler is experimental and spends budget on every prompt. Installing it
// hot would make `setup vscode` silently more expensive, so the opt-in gate is
// asserted rather than assumed.
func TestContextCompilerHookIsOptIn(t *testing.T) {
	if !contextCompilerHookIsOptIn() {
		t.Error("context-compiler.sh no longer gates on MAX_CONTEXT_AUTO_CONTEXT; it would run on every prompt")
	}
}

// A hook that fails mid-session is worse than no hook, and a syntax error only
// shows up at runtime, in someone else's editor.
func TestHookScriptsAreValidBash(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	entries, err := os.ReadDir(pluginHookScripts)
	if err != nil {
		t.Fatalf("read hook scripts: %v", err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			out, err := exec.Command("bash", "-n", filepath.Join(pluginHookScripts, e.Name())).CombinedOutput()
			if err != nil {
				t.Errorf("syntax error: %v\n%s", err, out)
			}
		})
	}
}

// Adding a script and forgetting to wire it leaves dead code that looks
// installed. Every script in the plugin directory must be referenced by
// hooks.json.
func TestPluginHooksJSONWiresEveryScript(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(pluginHookScripts, "..", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks.json: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("hooks.json is not valid JSON: %v", err)
	}
	entries, err := os.ReadDir(pluginHookScripts)
	if err != nil {
		t.Fatalf("read hook scripts: %v", err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		if !strings.Contains(string(raw), e.Name()) {
			t.Errorf("hooks/scripts/%s exists but hooks.json never runs it", e.Name())
		}
	}
}

// setup vscode is the only harness that installs hooks, and it must install
// every script its hooks.json references — a hook pointing at a missing file
// fails on the user's first prompt.
func TestSetupVSCodeInstallsEveryReferencedScript(t *testing.T) {
	root := t.TempDir()
	if err := applyHarness(t, "vscode", root, nil); err != nil {
		t.Fatalf("apply vscode: %v", err)
	}
	hooksJSON := filepath.Join(root, ".github", "hooks", "hooks.json")
	raw, err := os.ReadFile(hooksJSON)
	if err != nil {
		t.Fatalf("read installed hooks.json: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("installed hooks.json is not valid JSON: %v", err)
	}
	for _, script := range []string{"session-start.sh", "pre-compact.sh", "context-compiler.sh"} {
		if !strings.Contains(string(raw), script) {
			t.Errorf("hooks.json does not reference %s", script)
		}
		path := filepath.Join(root, ".github", "hooks", "scripts", script)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("hooks.json references %s but setup did not write it: %v", script, err)
			continue
		}
		if info.Mode().Perm()&0111 == 0 {
			t.Errorf("%s is not executable (mode %v); the hook cannot run", script, info.Mode().Perm())
		}
	}
}
