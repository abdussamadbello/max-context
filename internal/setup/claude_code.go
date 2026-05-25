package setup

import (
	"os"
	"path/filepath"
)

func setupClaudeCode(root string) error {
	ensureDir(filepath.Join(root, ".claude", "skills", "max-context"))
	skillPath := filepath.Join(root, ".claude", "skills", "max-context", "SKILL.md")
	if _, err := os.Stat(skillPath); os.IsNotExist(err) {
		os.WriteFile(skillPath, []byte("# Max Context\nUse query_codebase and get_architecture.\n"), 0644)
	}
	mcpPath := filepath.Join(root, ".mcp.json")
	if _, err := os.Stat(mcpPath); os.IsNotExist(err) {
		os.WriteFile(mcpPath, []byte(`{"mcpServers":{"max-context":{"command":"max-context","args":[]}}}`), 0644)
	}
	return appendWithMarkers(filepath.Join(root, "AGENTS.md"), "Use max-context: query_codebase, get_architecture.")
}
