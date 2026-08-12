package setup

import (
	"path/filepath"
)

func setupCursor(root string, r *Report) error {
	if err := mergeMCPConfig(filepath.Join(root, ".cursor", "mcp.json"), "mcpServers", r); err != nil {
		return err
	}
	rulePath := filepath.Join(root, ".cursor", "rules", "max-context.md")
	if err := writeFileIfAbsent(rulePath, "# Max Context\nUse query_codebase and get_architecture.\n", 0644, r); err != nil {
		return err
	}
	if err := writeCursorCommands(root, r); err != nil {
		return err
	}
	return appendWithMarkers(filepath.Join(root, "AGENTS.md"), "Use max-context: query_codebase, get_architecture.", r)
}
