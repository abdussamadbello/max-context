package arms

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"sync"

	"github.com/maxcontext/eval-locobench/internal/agent"
)

const contextToolName = "compile_context"

// ContextArm evaluates the context-compiler interaction proposed by the
// product strategy: the model gets one token-budgeted package compiled from the
// task, rather than choosing a sequence of retrieval tools itself.
type ContextArm struct {
	run    func(context.Context) ([]byte, error)
	mu     sync.Mutex
	called bool
}

// NewContextArm invokes the real CLI in the scenario project. The project has
// already been indexed by the harness, so this measures retrieval and package
// construction rather than indexing time.
func NewContextArm(mcBin, repoRoot, task string, budget int) *ContextArm {
	if budget <= 0 {
		budget = 4000
	}
	return &ContextArm{run: func(ctx context.Context) ([]byte, error) {
		cmd := exec.CommandContext(ctx, mcBin, "context", "--task", task, "--budget", strconv.Itoa(budget))
		cmd.Dir = repoRoot
		return cmd.CombinedOutput()
	}}
}

func (c *ContextArm) Tools() []agent.Tool {
	return []agent.Tool{{
		Name: contextToolName,
		Description: "Compile the repository evidence needed for the current task into one ranked, " +
			"token-budgeted context package. Call this once before answering.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	}}
}

func (c *ContextArm) Execute(ctx context.Context, name string, _ json.RawMessage) (string, bool) {
	if name != contextToolName {
		return "unknown tool: " + name, true
	}

	c.mu.Lock()
	if c.called {
		c.mu.Unlock()
		return "context package already supplied; answer from the existing package", true
	}
	c.called = true
	c.mu.Unlock()

	out, err := c.run(ctx)
	if err != nil {
		return fmt.Sprintf("context compiler failed: %v\n%s", err, string(out)), true
	}
	return string(out), false
}
