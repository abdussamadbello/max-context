package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const MarkerStart = "<!-- max-context start -->"
const MarkerEnd = "<!-- max-context end -->"

// gitignoreEntry keeps the per-project index DB and artifacts under
// .max-context/ out of version control.
const gitignoreEntry = ".max-context/"

var cliTargets = []string{"claude-code", "vscode", "codex", "antigravity", "cursor", "windsurf", "all"}

func Run(projectRoot string, target string) error {
	var ok bool
	for _, t := range cliTargets {
		if t == target {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("unknown setup target: %q", target)
	}
	// Regardless of CLI target, keep the index out of version control.
	_ = ensureGitignore(projectRoot)
	if target == "all" {
		for _, t := range cliTargets {
			if t == "all" {
				continue
			}
			runOne(projectRoot, t)
		}
		return nil
	}
	return runOne(projectRoot, target)
}

func runOne(root string, target string) error {
	switch target {
	case "claude-code":
		return setupClaudeCode(root)
	case "vscode":
		return setupVSCode(root)
	case "codex":
		return setupCodex(root)
	case "antigravity":
		return setupAntigravity(root)
	case "cursor":
		return setupCursor(root)
	case "windsurf":
		return setupWindsurf(root)
	}
	return nil
}

func appendWithMarkers(filePath, content string) error {
	existing, _ := os.ReadFile(filePath)
	s := string(existing)
	if strings.Contains(s, MarkerStart) {
		return nil
	}
	s = strings.TrimRight(s, "\n") + "\n\n" + MarkerStart + "\n" + content + "\n" + MarkerEnd + "\n"
	return os.WriteFile(filePath, []byte(s), 0644)
}

func ensureDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}

// ensureGitignore appends gitignoreEntry to the project's .gitignore unless
// .max-context is already ignored. Creates the file if absent. Idempotent.
func ensureGitignore(root string) error {
	path := filepath.Join(root, ".gitignore")
	existing, _ := os.ReadFile(path)
	for _, line := range strings.Split(string(existing), "\n") {
		switch strings.TrimSpace(line) {
		case ".max-context/", ".max-context", "/.max-context/", "/.max-context":
			return nil // already ignored in some accepted form
		}
	}
	s := string(existing)
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	s += gitignoreEntry + "\n"
	return os.WriteFile(path, []byte(s), 0644)
}
