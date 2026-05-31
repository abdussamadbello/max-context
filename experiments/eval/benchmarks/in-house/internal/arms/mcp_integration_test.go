package arms

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mcBinary returns the max-context binary path from MAX_CONTEXT_BIN, or skips.
func mcBinary(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("MAX_CONTEXT_BIN")
	if bin == "" {
		t.Skip("set MAX_CONTEXT_BIN to the max-context binary to run MCP integration tests")
	}
	if _, err := exec.LookPath(bin); err != nil {
		if _, err2 := os.Stat(bin); err2 != nil {
			t.Skipf("MAX_CONTEXT_BIN %q not found", bin)
		}
	}
	return bin
}

// TestMCPArm_EndToEnd indexes a tiny repo with the real binary, starts the MCP
// server over stdio, fetches the real tool list, and calls query_codebase.
func TestMCPArm_EndToEnd(t *testing.T) {
	bin := mcBinary(t)
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "app.py"),
		[]byte("class Conn:\n    def query(self):\n        pass\n"), 0o644)

	// Index the repo (real binary, --index builds then would watch; we run and
	// kill once the index exists).
	ctxIdx, cancelIdx := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelIdx()
	idx := exec.CommandContext(ctxIdx, bin, "--reindex")
	idx.Dir = root
	if out, err := idx.CombinedOutput(); err != nil {
		t.Fatalf("index failed: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := StartMCP(ctx, bin, root)
	if err != nil {
		t.Fatalf("StartMCP: %v", err)
	}
	defer client.Close()

	arm, err := NewMCPArm(ctx, client)
	if err != nil {
		t.Fatalf("NewMCPArm: %v", err)
	}

	// The arm must advertise max-context's 4 real tools.
	names := map[string]bool{}
	for _, tl := range arm.Tools() {
		names[tl.Name] = true
	}
	for _, want := range []string{"query_codebase", "get_call_chain", "get_impact", "get_architecture"} {
		if !names[want] {
			t.Errorf("missing advertised tool %q (got %v)", want, names)
		}
	}

	// Call query_codebase for the indexed symbol.
	out, isErr := arm.Execute(ctx, "query_codebase", json.RawMessage(`{"query":"query"}`))
	if isErr {
		t.Fatalf("query_codebase errored: %s", out)
	}
	if !strings.Contains(out, "app.py") {
		t.Errorf("expected app.py in result, got: %s", out)
	}
}
