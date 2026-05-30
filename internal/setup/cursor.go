package setup

import (
	"os"
	"path/filepath"
)

func setupCursor(root string) error {
	ensureDir(filepath.Join(root, ".cursor"))
	ensureDir(filepath.Join(root, ".cursor", "rules"))
	mcpPath := filepath.Join(root, ".cursor", "mcp.json")
	if _, e := os.Stat(mcpPath); os.IsNotExist(e) {
		os.WriteFile(mcpPath, []byte("{\"mcpServers\":{\"max-context\":{\"command\":\"max-context\",\"args\":[]}}}"), 0644)
	}
	rulePath := filepath.Join(root, ".cursor", "rules", "max-context.md")
	if _, e := os.Stat(rulePath); os.IsNotExist(e) {
		os.WriteFile(rulePath, []byte("# Max Context\nUse query_codebase and get_architecture.\n"), 0644)
	}
	if err := writeCursorCommands(root); err != nil {
		return err
	}
	return appendWithMarkers(filepath.Join(root, "AGENTS.md"), "Use max-context: query_codebase, get_architecture.")
}
