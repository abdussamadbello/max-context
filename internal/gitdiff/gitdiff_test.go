package gitdiff

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// requireGit skips the test when git is not installed.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

// initRepo creates a temp git repo with a single committed file, then dirties it.
func initRepo(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.go")
	run("commit", "-q", "-m", "initial")
	// Dirty: modify a.go, add b.go as untracked.
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n// changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("package a\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDiff_HEAD_ReturnsDirtyAndUntracked(t *testing.T) {
	requireGit(t)
	root := initRepo(t)

	paths, err := Diff(root, "HEAD")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	got := map[string]bool{}
	for _, p := range paths {
		got[p] = true
	}
	if !got["a.go"] {
		t.Fatalf("expected a.go in diff, got %v", paths)
	}
	if !got["b.go"] {
		t.Fatalf("expected untracked b.go in diff, got %v", paths)
	}
}

func TestDiff_NotARepo(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	_, err := Diff(root, "HEAD")
	if err == nil {
		t.Fatalf("expected error for non-repo, got nil")
	}
}
