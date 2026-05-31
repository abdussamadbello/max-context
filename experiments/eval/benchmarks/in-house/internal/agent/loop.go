package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// ToolExecutor runs a tool call for an arm (grep arm or max-context arm) and
// returns the textual result the model will see. The bool reports whether the
// call errored (surfaced to the model as an is_error tool_result).
type ToolExecutor interface {
	// Tools advertises the tool definitions for this arm.
	Tools() []Tool
	// Execute runs one tool call. Returns (resultText, isError).
	Execute(ctx context.Context, name string, input json.RawMessage) (string, bool)
}

// RunStatus is the terminal outcome of an agent run. Nothing is dropped: every
// run ends with exactly one status (project ethos — no silent caps).
type RunStatus string

const (
	StatusCompleted     RunStatus = "completed"      // model produced a final answer within budget
	StatusTruncatedTurn RunStatus = "truncated_turns" // hit the max-turn cap before finishing
	StatusToolLoop      RunStatus = "tool_loop"       // same tool+args repeated with no progress
	StatusRateLimited   RunStatus = "rate_limited"    // exhausted retries on 429
	StatusAPIError      RunStatus = "api_error"       // hard API/network error
	StatusBudgetHalt    RunStatus = "budget_halt"     // token cost guardrail tripped
)

// Turn is one model response in the transcript (for full auditability).
type Turn struct {
	Index        int            `json:"index"`
	Content      []ContentBlock `json:"content"`
	StopReason   string         `json:"stop_reason"`
	InputTokens  int            `json:"input_tokens"`
	OutputTokens int            `json:"output_tokens"`
	ToolCalls    []ToolCallLog  `json:"tool_calls,omitempty"`
}

// ToolCallLog records one tool invocation and its result, verbatim.
type ToolCallLog struct {
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
	Result  string          `json:"result"`
	IsError bool            `json:"is_error"`
}

// Result is the full record of an agent run.
type Result struct {
	Status          RunStatus `json:"status"`
	FinalAnswer     string    `json:"final_answer"`
	Turns           []Turn    `json:"turns"`
	TotalInTokens   int       `json:"total_input_tokens"`
	TotalOutTokens  int       `json:"total_output_tokens"`
	ToolCallCount   int       `json:"tool_call_count"`
	TurnsToAnswer   int       `json:"turns_to_answer"`
	WallClockMillis int64     `json:"wall_clock_ms"`
	Retries         int       `json:"retries"`
	ErrorDetail     string    `json:"error_detail,omitempty"` // populated on api_error/rate_limited
}

// Config controls one agent run.
type Config struct {
	Model       string
	MaxTokens   int           // per-call output cap
	Temperature float64       // 0 for task agents
	MaxTurns    int           // generous; truncation is logged, not hidden
	MaxRetries  int           // on rate-limit
	TokenBudget int           // total (in+out) guardrail; 0 = unlimited
	RetryWait   time.Duration // base backoff between retries
	// nowMillis is injected for deterministic tests; nil uses time.Now.
	nowMillis func() int64
}

// caller is the model-call surface the loop needs; *Client satisfies it, and
// tests inject a scripted stub.
//
// Caller is the model-call surface, satisfied by both the Anthropic API Client
// and the Bedrock client, so the loop and judge are backend-agnostic.
type Caller interface {
	Create(ctx context.Context, req CreateParams) (*CreateResult, error)
}

// Run drives the tool-use loop for one task on one arm. system is the combined
// system prompt (skeleton + arm playbook); task is the user task text.
func Run(ctx context.Context, c Caller, cfg Config, exec ToolExecutor, system, task string) *Result {
	res := &Result{Status: StatusCompleted}
	now := cfg.nowMillis
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	start := now()
	defer func() { res.WallClockMillis = now() - start }()

	tools := exec.Tools()
	msgs := []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: task}}}}

	var lastToolSig string
	repeatCount := 0

	for turn := 0; turn < cfg.MaxTurns; turn++ {
		out, err := callWithRetry(ctx, c, cfg, system, tools, msgs, res)
		if err != nil {
			res.Status = statusForError(err)
			res.ErrorDetail = err.Error()
			return res
		}

		res.TotalInTokens += out.Usage.InputTokens
		res.TotalOutTokens += out.Usage.OutputTokens
		if cfg.TokenBudget > 0 && res.TotalInTokens+res.TotalOutTokens > cfg.TokenBudget {
			res.Status = StatusBudgetHalt
			recordTurn(res, turn, out, nil)
			return res
		}

		// Collect tool_use blocks; assistant message is appended verbatim.
		msgs = append(msgs, Message{Role: "assistant", Content: out.Content})
		toolUses := filterToolUse(out.Content)

		if len(toolUses) == 0 || out.StopReason == "end_turn" {
			// Final answer: concatenate text blocks.
			res.FinalAnswer = concatText(out.Content)
			res.TurnsToAnswer = turn + 1
			recordTurn(res, turn, out, nil)
			res.Status = StatusCompleted
			return res
		}

		// Loop guard: identical tool+args repeated 3× in a row with no progress.
		sig := toolSignature(toolUses)
		if sig == lastToolSig {
			repeatCount++
			if repeatCount >= 2 {
				recordTurn(res, turn, out, nil)
				res.Status = StatusToolLoop
				return res
			}
		} else {
			repeatCount = 0
			lastToolSig = sig
		}

		// Execute each tool call, build tool_result blocks.
		var resultBlocks []ContentBlock
		var callLogs []ToolCallLog
		for _, tu := range toolUses {
			text, isErr := exec.Execute(ctx, tu.Name, tu.Input)
			res.ToolCallCount++
			resultBlocks = append(resultBlocks, ContentBlock{
				Type: "tool_result", ToolUseID: tu.ID, Content: text, IsError: isErr,
			})
			callLogs = append(callLogs, ToolCallLog{Name: tu.Name, Input: tu.Input, Result: text, IsError: isErr})
		}
		recordTurn(res, turn, out, callLogs)
		msgs = append(msgs, Message{Role: "user", Content: resultBlocks})
	}

	res.Status = StatusTruncatedTurn
	return res
}

func callWithRetry(ctx context.Context, c Caller, cfg Config, system string, tools []Tool, msgs []Message, res *Result) (*CreateResult, error) {
	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		out, err := c.Create(ctx, CreateParams{
			Model: cfg.Model, MaxTokens: cfg.MaxTokens, Temperature: cfg.Temperature,
			System: system, Tools: tools, Messages: msgs,
		})
		if err == nil {
			return out, nil
		}
		lastErr = err
		ae, ok := err.(*APIError)
		if !ok || ae.Category != "rate_limited" {
			return nil, err // hard error: don't retry
		}
		res.Retries++
		// Honor the server's Retry-After exactly when provided; otherwise use
		// linear backoff. Respecting Retry-After is the correct way to ride out a
		// low token-per-minute ceiling without thrashing.
		wait := cfg.RetryWait * time.Duration(attempt+1)
		if secs := parseRetryAfter(ae.RetryAfter); secs > 0 {
			wait = time.Duration(secs)*time.Second + time.Second // +1s safety margin
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, lastErr
}

// parseRetryAfter parses an integer "Retry-After" seconds value; 0 if absent.
func parseRetryAfter(s string) int {
	if s == "" {
		return 0
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func statusForError(err error) RunStatus {
	if ae, ok := err.(*APIError); ok && ae.Category == "rate_limited" {
		return StatusRateLimited
	}
	return StatusAPIError
}

func recordTurn(res *Result, idx int, out *CreateResult, calls []ToolCallLog) {
	res.Turns = append(res.Turns, Turn{
		Index: idx, Content: out.Content, StopReason: out.StopReason,
		InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens, ToolCalls: calls,
	})
}

func filterToolUse(blocks []ContentBlock) []ContentBlock {
	var out []ContentBlock
	for _, b := range blocks {
		if b.Type == "tool_use" {
			out = append(out, b)
		}
	}
	return out
}

func concatText(blocks []ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

func toolSignature(toolUses []ContentBlock) string {
	var b strings.Builder
	for _, tu := range toolUses {
		b.WriteString(tu.Name)
		b.WriteByte('(')
		b.Write(tu.Input)
		b.WriteByte(')')
	}
	return b.String()
}
