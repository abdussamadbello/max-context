package arms

import (
	"context"
	"encoding/json"

	"github.com/maxcontext/eval-locobench/internal/agent"
)

// HybridArm is the REALISTIC deployment arm: it exposes the full baseline file
// toolset (grep / read_file / list_files) AND max-context's MCP tools at the
// same time — exactly what an MCP host like Claude Code or Cursor looks like
// once max-context is installed (MCP tools are additive; they never remove the
// agent's built-in file tools).
//
// Why it exists: the pure MCPArm measures a configuration nobody ships (an agent
// that can ONLY use max-context and cannot read files). That handicap turns the
// index's non-code coverage gap into hard zeros. The honest product question is
// not "grep vs mc" but "grep+read vs grep+read+mc" — does max-context add value
// ON TOP OF the file tools an agent already has? HybridArm is the "+mc" side; the
// only difference from the GrepArm baseline is the presence of the 5 MC tools, so
// any score/cost delta is max-context's marginal contribution.
type HybridArm struct {
	grep  *GrepArm
	mcp   *MCPArm
	tools []agent.Tool
	// route maps each tool name to the executor that owns it, so Execute never
	// has to re-derive ownership (and so a name collision would be caught at
	// construction, not silently shadowed).
	route map[string]agent.ToolExecutor
}

// NewHybridArm composes a grep arm and an MCP arm into one executor. It fails if
// the two advertise a tool of the same name (today they don't — grep/read_file/
// list_files vs get_definition/query_codebase/get_call_chain/get_impact/
// get_architecture — but this guards against future drift rather than silently
// letting one shadow the other).
func NewHybridArm(grep *GrepArm, mcp *MCPArm) (*HybridArm, error) {
	h := &HybridArm{grep: grep, mcp: mcp, route: map[string]agent.ToolExecutor{}}
	for _, src := range []agent.ToolExecutor{grep, mcp} {
		for _, t := range src.Tools() {
			if _, dup := h.route[t.Name]; dup {
				return nil, &dupToolError{name: t.Name}
			}
			h.route[t.Name] = src
			h.tools = append(h.tools, t)
		}
	}
	return h, nil
}

func (h *HybridArm) Tools() []agent.Tool { return h.tools }

func (h *HybridArm) Execute(ctx context.Context, name string, input json.RawMessage) (string, bool) {
	exec, ok := h.route[name]
	if !ok {
		return "unknown tool: " + name, true
	}
	return exec.Execute(ctx, name, input)
}

type dupToolError struct{ name string }

func (e *dupToolError) Error() string {
	return "hybrid arm: duplicate tool name across grep and mcp toolsets: " + e.name
}
