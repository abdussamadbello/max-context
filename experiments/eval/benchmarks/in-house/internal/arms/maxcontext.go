package arms

import (
	"context"
	"encoding/json"

	"github.com/maxcontext/eval/internal/agent"
)

// MCPArm is the max-context arm. It advertises the EXACT tools the running
// max-context server publishes (fetched via tools/list) and forwards each tool
// call over stdio MCP — the real integration path.
type MCPArm struct {
	client *MCPClient
	tools  []agent.Tool
}

// NewMCPArm wraps a started MCP client, fetching its advertised tool list so the
// model sees precisely what max-context offers (no hardcoded schema drift).
func NewMCPArm(ctx context.Context, client *MCPClient) (*MCPArm, error) {
	defs, err := client.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	tools := make([]agent.Tool, 0, len(defs))
	for _, d := range defs {
		schema := d.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		tools = append(tools, agent.Tool{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: schema,
		})
	}
	return &MCPArm{client: client, tools: tools}, nil
}

func (m *MCPArm) Tools() []agent.Tool { return m.tools }

func (m *MCPArm) Execute(ctx context.Context, name string, input json.RawMessage) (string, bool) {
	out, err := m.client.CallTool(ctx, name, input)
	if err != nil {
		// Surface the error to the model as an is_error tool_result; the run log
		// separately records stdio/server failures at the harness level.
		if out != "" {
			return out, true
		}
		return "tool call failed: " + err.Error(), true
	}
	return out, false
}
