package agent

import (
	"context"
	"encoding/json"
	"testing"
)

// fakeExec is a scripted ToolExecutor: it always returns the same result.
type fakeExec struct {
	tools  []Tool
	result string
}

func (f fakeExec) Tools() []Tool { return f.tools }
func (f fakeExec) Execute(_ context.Context, _ string, _ json.RawMessage) (string, bool) {
	return f.result, false
}

// scriptedClient returns a pre-baked sequence of responses, ignoring input.
type scriptedClient struct {
	responses []*CreateResult
	i         int
}

type transientThenSuccessClient struct{ calls int }

func (c *transientThenSuccessClient) Create(_ context.Context, _ CreateParams) (*CreateResult, error) {
	c.calls++
	if c.calls == 1 {
		return nil, &APIError{Category: "transient", Message: "temporary upstream failure"}
	}
	return textResp("recovered", "end_turn"), nil
}

func (s *scriptedClient) Create(_ context.Context, _ CreateParams) (*CreateResult, error) {
	r := s.responses[s.i]
	if s.i < len(s.responses)-1 {
		s.i++
	}
	return r, nil
}

func textResp(text, stop string) *CreateResult {
	return &CreateResult{Content: []ContentBlock{{Type: "text", Text: text}}, StopReason: stop, Usage: Usage{InputTokens: 10, OutputTokens: 5}}
}

func toolResp(id, name, input string) *CreateResult {
	return &CreateResult{
		Content:    []ContentBlock{{Type: "tool_use", ID: id, Name: name, Input: json.RawMessage(input)}},
		StopReason: "tool_use",
		Usage:      Usage{InputTokens: 10, OutputTokens: 5},
	}
}

func baseCfg() Config {
	return Config{Model: "test", MaxTokens: 100, Temperature: 0, MaxTurns: 5, MaxRetries: 0, nowMillis: func() int64 { return 0 }}
}

// Full loop: model calls a tool once, then answers. Status=completed.
func TestRun_ToolThenAnswer(t *testing.T) {
	sc := &scriptedClient{responses: []*CreateResult{
		toolResp("t1", "grep", `{"q":"Foo"}`),
		textResp("The answer is Foo in foo.go", "end_turn"),
	}}
	exec := fakeExec{tools: []Tool{{Name: "grep"}}, result: "foo.go:1:func Foo"}
	res := Run(context.Background(), sc, baseCfg(), exec, "sys", "where is Foo?")

	if res.Status != StatusCompleted {
		t.Fatalf("status=%s, want completed", res.Status)
	}
	if res.ToolCallCount != 1 {
		t.Errorf("tool calls=%d, want 1", res.ToolCallCount)
	}
	if res.FinalAnswer != "The answer is Foo in foo.go" {
		t.Errorf("answer=%q", res.FinalAnswer)
	}
	if res.TotalInTokens != 20 || res.TotalOutTokens != 10 {
		t.Errorf("usage in=%d out=%d, want 20/10", res.TotalInTokens, res.TotalOutTokens)
	}
}

// Model repeats the identical tool call -> loop guard fires.
func TestRun_ToolLoopGuard(t *testing.T) {
	loopCall := toolResp("t1", "grep", `{"q":"x"}`)
	sc := &scriptedClient{responses: []*CreateResult{loopCall, loopCall, loopCall, loopCall}}
	exec := fakeExec{tools: []Tool{{Name: "grep"}}, result: "nothing"}
	res := Run(context.Background(), sc, baseCfg(), exec, "sys", "task")
	if res.Status != StatusToolLoop {
		t.Fatalf("status=%s, want tool_loop", res.Status)
	}
}

// Model never stops calling tools (distinct args) -> truncated at MaxTurns.
func TestRun_TruncatedTurns(t *testing.T) {
	// distinct args each turn so the loop guard doesn't fire
	resps := []*CreateResult{}
	for i := 0; i < 10; i++ {
		resps = append(resps, toolResp("t", "grep", `{"q":"`+string(rune('a'+i))+`"}`))
	}
	sc := &scriptedClient{responses: resps}
	exec := fakeExec{tools: []Tool{{Name: "grep"}}, result: "x"}
	cfg := baseCfg()
	cfg.MaxTurns = 3
	res := Run(context.Background(), sc, cfg, exec, "sys", "task")
	if res.Status != StatusTruncatedTurn {
		t.Fatalf("status=%s, want truncated_turns", res.Status)
	}
}

func TestRun_TruncatedModelResponse(t *testing.T) {
	sc := &scriptedClient{responses: []*CreateResult{textResp("partial", "max_tokens")}}
	res := Run(context.Background(), sc, baseCfg(), fakeExec{}, "sys", "task")
	if res.Status != StatusTruncatedTurn || res.FinalAnswer != "" {
		t.Fatalf("result = %#v", res)
	}
}

// Token budget guardrail halts the run.
func TestRun_BudgetHalt(t *testing.T) {
	resps := []*CreateResult{}
	for i := 0; i < 10; i++ {
		resps = append(resps, toolResp("t", "grep", `{"q":"`+string(rune('a'+i))+`"}`))
	}
	sc := &scriptedClient{responses: resps}
	exec := fakeExec{tools: []Tool{{Name: "grep"}}, result: "x"}
	cfg := baseCfg()
	cfg.TokenBudget = 20 // each turn adds 15; trips on turn 2
	res := Run(context.Background(), sc, cfg, exec, "sys", "task")
	if res.Status != StatusBudgetHalt {
		t.Fatalf("status=%s, want budget_halt", res.Status)
	}
}

func TestRun_RetriesTransientProviderFailure(t *testing.T) {
	client := &transientThenSuccessClient{}
	cfg := baseCfg()
	cfg.MaxRetries = 1
	res := Run(context.Background(), client, cfg, fakeExec{}, "sys", "task")
	if res.Status != StatusCompleted || res.FinalAnswer != "recovered" {
		t.Fatalf("result = %#v", res)
	}
	if res.Retries != 1 || client.calls != 2 {
		t.Fatalf("retries=%d calls=%d", res.Retries, client.calls)
	}
}

func TestConcatTextAndToolFilter(t *testing.T) {
	blocks := []ContentBlock{
		{Type: "text", Text: "hello "},
		{Type: "tool_use", ID: "1", Name: "grep", Input: json.RawMessage(`{}`)},
		{Type: "text", Text: "world"},
	}
	if got := concatText(blocks); got != "hello world" {
		t.Errorf("concatText = %q", got)
	}
	if tu := filterToolUse(blocks); len(tu) != 1 || tu[0].Name != "grep" {
		t.Errorf("filterToolUse = %+v", tu)
	}
}

func TestToolSignatureStable(t *testing.T) {
	a := []ContentBlock{{Type: "tool_use", Name: "grep", Input: json.RawMessage(`{"q":"x"}`)}}
	b := []ContentBlock{{Type: "tool_use", Name: "grep", Input: json.RawMessage(`{"q":"x"}`)}}
	if toolSignature(a) != toolSignature(b) {
		t.Error("identical tool calls should have equal signatures")
	}
	c := []ContentBlock{{Type: "tool_use", Name: "grep", Input: json.RawMessage(`{"q":"y"}`)}}
	if toolSignature(a) == toolSignature(c) {
		t.Error("different args should differ")
	}
}
