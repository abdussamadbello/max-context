package setup

import (
	"os"
	"path/filepath"
)

func setupCodex(root string) error {
	ensureDir(filepath.Join(root, ".codex", "skills", "max-context"))
	skillPath := filepath.Join(root, ".codex", "skills", "max-context", "SKILL.md")
	if _, err := os.Stat(skillPath); os.IsNotExist(err) {
		os.WriteFile(skillPath, []byte("# Max Context\nUse query_codebase and get_architecture.\n"), 0644)
	}
	return appendWithMarkers(filepath.Join(root, "AGENTS.md"), "Use max-context: query_codebase, get_architecture.")
}
