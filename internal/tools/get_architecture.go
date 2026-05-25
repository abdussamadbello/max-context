package tools

import (
	"encoding/json"
	"path/filepath"

	"github.com/maxcontext/max-context/internal/artifacts"
	"github.com/maxcontext/max-context/internal/mcp"
)

type getArchitectureArgs struct {
	Focus string `json:"focus"`
}

func GetArchitectureHandler(projectRoot string) mcp.ToolHandler {
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
		return []mcp.ContentItem{{Type: "text", Text: text}}, nil
	}
}
