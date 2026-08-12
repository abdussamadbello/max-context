package setup

import (
	"path/filepath"
)

func setupClaudeCode(root string, r *Report) error {
	skillPath := filepath.Join(root, ".claude", "skills", "max-context", "SKILL.md")
	if err := writeFileIfAbsent(skillPath, "# Max Context\nUse query_codebase and get_architecture.\n", 0644, r); err != nil {
		return err
	}
	if err := mergeMCPConfig(filepath.Join(root, ".mcp.json"), "mcpServers", r); err != nil {
		return err
	}
	if err := writeClaudeCommands(root, r); err != nil {
		return err
	}
	return appendWithMarkers(filepath.Join(root, "AGENTS.md"), "Use max-context: query_codebase, get_architecture.", r)
}
