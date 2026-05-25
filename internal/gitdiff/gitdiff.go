// Package gitdiff produces the set of changed files for a given git revision range,
// including untracked files so that "what does my change touch?" returns the right
// answer mid-edit.
package gitdiff

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Diff returns project-root-relative file paths that differ between the working
// tree and the given revision (typical: "HEAD"). Includes:
//   - tracked-modified files (git diff --name-only)
//   - staged-modified files (git diff --cached --name-only)
//   - untracked files not in .gitignore (git ls-files --others --exclude-standard)
//
// Returns an error if root is not a git repository or git is not on PATH.
// Returns an empty slice (not nil) if there are no changes.
func Diff(root string, rev string) ([]string, error) {
	if rev == "" {
		rev = "HEAD"
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("git not installed")
	}
	// Verify it's a git repo.
	check := exec.Command("git", "rev-parse", "--git-dir")
	check.Dir = root
	if out, err := check.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("not a git repository: %s", strings.TrimSpace(string(out)))
	}

	seen := map[string]struct{}{}
	collect := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git %v: %s", args, strings.TrimSpace(stderr.String()))
		}
		for _, line := range strings.Split(stdout.String(), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// Normalize to forward slashes for consistency with indexed file_path values.
			line = filepath.ToSlash(line)
			seen[line] = struct{}{}
		}
		return nil
	}

	if err := collect("diff", "--name-only", rev); err != nil {
		return nil, err
	}
	if err := collect("diff", "--cached", "--name-only", rev); err != nil {
		return nil, err
	}
	if err := collect("ls-files", "--others", "--exclude-standard"); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	return out, nil
}
