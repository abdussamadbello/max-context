package tools

import (
	"database/sql"
	"encoding/json"
	"path/filepath"

	"github.com/maxcontext/max-context/internal/artifacts"
	"github.com/maxcontext/max-context/internal/mcp"
)

type getArchitectureArgs struct {
	Focus string `json:"focus"`
}

func GetArchitectureHandler(database *sql.DB, projectRoot string) mcp.ToolHandler {
	dir := filepath.Join(projectRoot, ".max-context")
	return func(args json.RawMessage) (interface{}, error) {
		summary, err := artifacts.ReadSummary(dir)
		if err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeIndexNotReady, Message: "architecture not ready; run index first"}
		}
		arch, _ := artifacts.ReadArchitecture(dir)
		text := summary
		if arch != "" {
			text = summary + "\n\n" + arch
		}
		// get_architecture returns prose (not JSON), so surface staleness as an
		// inline warning line when the index is unhealthy — keeping the text shape
		// backward compatible.
		if _, warning := stalenessInfo(database); warning != "" {
			text += "\n\n[staleness] " + warning
		}
		return []mcp.ContentItem{{Type: "text", Text: text}}, nil
	}
}
