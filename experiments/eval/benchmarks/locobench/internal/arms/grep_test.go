package arms

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func tempRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"app.py":       "class Conn:\n    def query(self):\n        pass\n",
		"sub/util.py":  "def helper():\n    return 1\n",
		"readme.md":    "# project\nthis explains why\n",
	}
	for rel, content := range files {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	return root
}

func TestGrepArm_Search(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not available")
	}
	g := NewGrepArm(tempRepo(t))
	out, isErr := g.Execute(context.Background(), "grep", json.RawMessage(`{"pattern":"def query"}`))
	if isErr {
		t.Fatalf("grep errored: %s", out)
	}
	if !strings.Contains(out, "app.py") || !strings.Contains(out, "def query") {
		t.Errorf("expected app.py match, got: %s", out)
	}
}

func TestGrepArm_SearchWithFlags(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not available")
	}
	g := NewGrepArm(tempRepo(t))
	// type filter + word boundary
	out, isErr := g.Execute(context.Background(), "grep", json.RawMessage(`{"pattern":"helper","args":["-t","py","-w"]}`))
	if isErr {
		t.Fatalf("grep errored: %s", out)
	}
	if !strings.Contains(out, "util.py") {
		t.Errorf("expected util.py, got: %s", out)
	}
}

func TestGrepArm_ReadFileRange(t *testing.T) {
	g := NewGrepArm(tempRepo(t))
	out, isErr := g.Execute(context.Background(), "read_file", json.RawMessage(`{"path":"app.py","start":2,"end":2}`))
	if isErr {
		t.Fatalf("read errored: %s", out)
	}
	if !strings.Contains(out, "def query") || strings.Contains(out, "class Conn") {
		t.Errorf("range read wrong: %s", out)
	}
}

func TestGrepArm_PathEscapeBlocked(t *testing.T) {
	g := NewGrepArm(tempRepo(t))
	out, isErr := g.Execute(context.Background(), "read_file", json.RawMessage(`{"path":"../../../etc/passwd"}`))
	if !isErr {
		t.Errorf("expected escape to be blocked, got: %s", out)
	}
}

func TestGrepArm_ListFiles(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not available")
	}
	g := NewGrepArm(tempRepo(t))
	out, isErr := g.Execute(context.Background(), "list_files", json.RawMessage(`{"glob":"**/*.py"}`))
	if isErr {
		t.Fatalf("list errored: %s", out)
	}
	if !strings.Contains(out, "app.py") || !strings.Contains(out, "util.py") || strings.Contains(out, "readme.md") {
		t.Errorf("glob listing wrong: %s", out)
	}
}
