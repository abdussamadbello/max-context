package setup

import (
	"os"
	"path/filepath"
)

func setupWindsurf(root string) error {
	dir := filepath.Join(root, ".windsurf", "rules")
	if err := ensureDir(dir); err != nil {
		return err
	}
	rulePath := filepath.Join(dir, "max-context.md")
	ruleContent := "# Max Context\n\nPrefer query_codebase over grep. Use get_architecture for project overview.\n"
	if _, err := os.Stat(rulePath); os.IsNotExist(err) {
		if err := os.WriteFile(rulePath, []byte(ruleContent), 0644); err != nil {
			return err
		}
	}
	workflowDir := filepath.Join(root, ".windsurf", "workflows")
	if err := ensureDir(workflowDir); err != nil {
		return err
	}
	workflowPath := filepath.Join(workflowDir, "reindex.md")
	workflowContent := "# Reindex\n\nRun `max-context --reindex` to rebuild the index.\n"
	if _, err := os.Stat(workflowPath); os.IsNotExist(err) {
		if err := os.WriteFile(workflowPath, []byte(workflowContent), 0644); err != nil {
			return err
		}
	}
	agentsPath := filepath.Join(root, "AGENTS.md")
	return appendWithMarkers(agentsPath, "Use max-context MCP: query_codebase, get_architecture.")
}
