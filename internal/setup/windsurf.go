package setup

import (
	"path/filepath"
)

func setupWindsurf(root string, r *Report) error {
	rulePath := filepath.Join(root, ".windsurf", "rules", "max-context.md")
	ruleContent := "# Max Context\n\nPrefer query_codebase over grep. Use get_architecture for project overview.\n"
	if err := writeFileIfAbsent(rulePath, ruleContent, 0644, r); err != nil {
		return err
	}
	if err := writeWindsurfCommands(root, r); err != nil {
		return err
	}
	return appendWithMarkers(filepath.Join(root, "AGENTS.md"), "Use max-context MCP: query_codebase, get_architecture.", r)
}
