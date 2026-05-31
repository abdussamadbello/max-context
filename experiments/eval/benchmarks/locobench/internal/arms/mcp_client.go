package arms

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// MCPClient speaks JSON-RPC 2.0 over stdio to a long-running `max-context` MCP
// server process. This is the REAL integration path users use — not an
// in-process shortcut — so the experiment measures what ships.
type MCPClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	mu     sync.Mutex
	nextID int
}

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("rpc %d: %s", e.Code, e.Message) }

// StartMCP launches `binary` (the max-context server) with cwd=repoRoot and
// performs the MCP initialize handshake. The server indexes/serves repoRoot.
func StartMCP(ctx context.Context, binary, repoRoot string) (*MCPClient, error) {
	cmd := exec.CommandContext(ctx, binary)
	cmd.Dir = repoRoot
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	// stderr is left attached to the parent for visibility into server crashes.
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start max-context: %w", err)
	}
	c := &MCPClient{cmd: cmd, stdin: stdin, stdout: bufio.NewReaderSize(stdout, 1<<20), nextID: 1}

	// initialize handshake
	if _, err := c.call(ctx, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "max-context-eval", "version": "0"},
	}); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}
	return c, nil
}

// MCPToolDef is a tool advertised by the server's tools/list.
type MCPToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ListTools fetches the server's advertised tools, so the arm advertises EXACTLY
// what the running max-context publishes (no schema drift / hardcoding).
func (c *MCPClient) ListTools(ctx context.Context) ([]MCPToolDef, error) {
	raw, err := c.call(ctx, "tools/list", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var result struct {
		Tools []MCPToolDef `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode tools/list: %w", err)
	}
	return result.Tools, nil
}

// CallTool invokes an MCP tool by name and returns the concatenated text of the
// content result — exactly what an MCP host would feed back to the model.
func (c *MCPClient) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	raw, err := c.call(ctx, "tools/call", map[string]interface{}{
		"name":      name,
		"arguments": json.RawMessage(args),
	})
	if err != nil {
		return "", err
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("decode tool result: %w", err)
	}
	var s string
	for _, ci := range result.Content {
		s += ci.Text
	}
	if result.IsError {
		return s, fmt.Errorf("tool reported error")
	}
	return s, nil
}

// call sends one request and blocks for the matching response. A per-call
// deadline guards against stdio hangs (recorded as mcp_stdio_failure upstream).
func (c *MCPClient) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := c.nextID
	c.nextID++
	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	line, _ := json.Marshal(req)
	line = append(line, '\n')

	type readResult struct {
		raw json.RawMessage
		err error
	}
	done := make(chan readResult, 1)
	go func() {
		if _, err := c.stdin.Write(line); err != nil {
			done <- readResult{err: fmt.Errorf("write: %w", err)}
			return
		}
		// Read until we see the response with our id (skip notifications).
		for {
			respLine, err := c.stdout.ReadBytes('\n')
			if err != nil {
				done <- readResult{err: fmt.Errorf("read: %w", err)}
				return
			}
			var resp rpcResponse
			if err := json.Unmarshal(respLine, &resp); err != nil {
				continue // skip non-JSON / log lines
			}
			if resp.ID != id {
				continue
			}
			if resp.Error != nil {
				done <- readResult{err: resp.Error}
				return
			}
			done <- readResult{raw: resp.Result}
			return
		}
	}()

	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-deadline.C:
		return nil, fmt.Errorf("mcp call %q timed out", method)
	case r := <-done:
		return r.raw, r.err
	}
}

// Close terminates the server process.
func (c *MCPClient) Close() error {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
	return nil
}
