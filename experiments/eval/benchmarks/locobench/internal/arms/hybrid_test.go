package arms

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/maxcontext/eval-locobench/internal/agent"
)

// stubExec is a minimal ToolExecutor for testing composition without a live MCP
// server. It advertises the given tool names and echoes which tool ran.
type stubExec struct {
	names []string
	ran   *string
}

func (s stubExec) Tools() []agent.Tool {
	out := make([]agent.Tool, len(s.names))
	for i, n := range s.names {
		out[i] = agent.Tool{Name: n, InputSchema: json.RawMessage(`{"type":"object"}`)}
	}
	return out
}

func (s stubExec) Execute(_ context.Context, name string, _ json.RawMessage) (string, bool) {
	*s.ran = name
	return "ran:" + name, false
}

func TestHybridMergesAndRoutes(t *testing.T) {
	var gotGrep, gotMC string
	grepLike := stubExec{names: []string{"grep", "read_file", "list_files"}, ran: &gotGrep}
	mcLike := stubExec{names: []string{"get_definition", "query_codebase"}, ran: &gotMC}

	// NewHybridArm needs concrete *GrepArm/*MCPArm; test the composition logic
	// directly via the same merge path by constructing through the executors.
	h := &HybridArm{route: map[string]agent.ToolExecutor{}}
	for _, src := range []agent.ToolExecutor{grepLike, mcLike} {
		for _, tl := range src.Tools() {
			if _, dup := h.route[tl.Name]; dup {
				t.Fatalf("unexpected dup %s", tl.Name)
			}
			h.route[tl.Name] = src
			h.tools = append(h.tools, tl)
		}
	}

	if len(h.Tools()) != 5 {
		t.Fatalf("merged tool count = %d, want 5", len(h.Tools()))
	}
	// Route a file tool → grep executor; a symbol tool → mc executor.
	if out, isErr := h.Execute(context.Background(), "read_file", nil); isErr || gotGrep != "read_file" || out != "ran:read_file" {
		t.Errorf("read_file routed wrong: out=%q isErr=%v gotGrep=%q", out, isErr, gotGrep)
	}
	if out, isErr := h.Execute(context.Background(), "get_definition", nil); isErr || gotMC != "get_definition" || out != "ran:get_definition" {
		t.Errorf("get_definition routed wrong: out=%q isErr=%v gotMC=%q", out, isErr, gotMC)
	}
	// Unknown tool → error.
	if _, isErr := h.Execute(context.Background(), "nonexistent", nil); !isErr {
		t.Error("unknown tool should return isError=true")
	}
}
