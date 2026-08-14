package ceiling

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/maxcontext/eval/internal/arms"
)

// RunMaxContext indexes a working copy of the repo and executes the probe's
// declared tool calls over real stdio MCP — the same path a host uses — then
// harvests every symbol name the responses contain.
//
// Harvesting all names rather than a hand-picked field is the counterpart to
// giving grep every hit: each arm is credited with everything its output puts in
// front of the model, and charged for the bytes that took.
func RunMaxContext(ctx context.Context, workRoot string, p Probe, binary string) ArmResult {
	res := ArmResult{Arm: ArmMaxContext}

	if err := indexRepo(ctx, binary, workRoot); err != nil {
		res.Err = err.Error()
		res.finalize(p.ExpectedSymbols)
		return res
	}

	client, err := arms.StartMCP(ctx, binary, workRoot)
	if err != nil {
		res.Err = fmt.Sprintf("start mcp: %v", err)
		res.finalize(p.ExpectedSymbols)
		return res
	}
	defer client.Close()

	calls := p.MCCalls
	if len(calls) == 0 {
		args, _ := json.Marshal(map[string]interface{}{
			"function_name": p.Symbol, "direction": "callers", "depth": 3,
		})
		calls = []MCCall{{Tool: "get_call_chain", Args: args}}
	}

	for _, c := range calls {
		args := c.Args
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		out, err := client.CallTool(ctx, c.Tool, args)
		res.ToolCalls++
		res.OutputBytes += len(out)
		res.Steps = append(res.Steps, Step{
			Arm:    ArmMaxContext,
			Detail: c.Tool + " " + string(args),
			Bytes:  len(out),
		})
		if err != nil {
			res.Err = fmt.Sprintf("%s: %v", c.Tool, err)
			continue
		}
		res.Predicted = append(res.Predicted, harvestNames(out)...)
	}

	res.finalize(p.ExpectedSymbols)
	return res
}

// indexRepo builds the max-context index for workRoot, failing loudly. An
// unindexed repo would return empty answers that score as a total loss, which
// looks like a finding rather than the setup error it is.
func indexRepo(ctx context.Context, binary, workRoot string) error {
	cmd := exec.CommandContext(ctx, binary, "--index", "--project", workRoot)
	cmd.Dir = workRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("index %s: %v: %s", workRoot, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// harvestNames pulls every "name" value out of a tool's JSON response, at any
// depth. Falls back to nothing if the response is not JSON — max-context's tools
// return JSON text, so a non-JSON body is a failure worth scoring as one.
func harvestNames(body string) []string {
	var v interface{}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return nil
	}
	var out []string
	var walk func(interface{})
	walk = func(n interface{}) {
		switch t := n.(type) {
		case map[string]interface{}:
			for k, val := range t {
				if k == "name" {
					if s, ok := val.(string); ok {
						out = append(out, s)
					}
				}
				walk(val)
			}
		case []interface{}:
			for _, item := range t {
				walk(item)
			}
		}
	}
	walk(v)
	return out
}

// CopyDir copies a fixture into a scratch working directory, so indexing never
// writes .max-context/ into the committed tree.
func CopyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode().Perm())
	})
}
