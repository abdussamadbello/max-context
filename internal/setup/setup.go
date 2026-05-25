package setup

import (
	"fmt"
	"os"
	"strings"
)

const MarkerStart = "<!-- max-context start -->"
const MarkerEnd = "<!-- max-context end -->"

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
