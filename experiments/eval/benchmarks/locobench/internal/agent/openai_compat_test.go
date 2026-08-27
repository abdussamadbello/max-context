package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatCreateTranslatesToolConversation(t *testing.T) {
	t.Parallel()

	var got openAIRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if auth := r.Header.Get("authorization"); auth != "Bearer test-key" {
			t.Errorf("authorization = %q", auth)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":null,"tool_calls":[{
				"id":"call_2","type":"function","function":{"name":"lookup","arguments":"{\"symbol\":\"Bar\"}"}
			}]},"finish_reason":"tool_calls"}],
			"usage":{"prompt_tokens":31,"completion_tokens":7}
		}`))
	}))
	defer server.Close()

	client := NewOpenAICompatClient(server.URL, "test-key")
	result, err := client.Create(context.Background(), CreateParams{
		Model: "test-model", MaxTokens: 256, System: "system prompt",
		Tools: []Tool{{Name: "lookup", Description: "Find a symbol", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Messages: []Message{
			{Role: "user", Content: []ContentBlock{{Type: "text", Text: "find Foo"}}},
			{Role: "assistant", Content: []ContentBlock{{Type: "tool_use", ID: "call_1", Name: "lookup", Input: json.RawMessage(`{"symbol":"Foo"}`)}}},
			{Role: "user", Content: []ContentBlock{{Type: "tool_result", ToolUseID: "call_1", Content: "Foo is in foo.go"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Messages) != 4 {
		t.Fatalf("messages = %#v", got.Messages)
	}
	if got.Messages[0].Role != "system" || got.Messages[1].Role != "user" {
		t.Fatalf("leading messages = %#v", got.Messages[:2])
	}
	if len(got.Messages[2].ToolCalls) != 1 || got.Messages[2].ToolCalls[0].Function.Arguments != `{"symbol":"Foo"}` {
		t.Fatalf("assistant tool call = %#v", got.Messages[2])
	}
	if got.Messages[3].Role != "tool" || got.Messages[3].ToolCallID != "call_1" {
		t.Fatalf("tool result = %#v", got.Messages[3])
	}
	if len(got.Tools) != 1 || string(got.Tools[0].Function.Parameters) != `{"type":"object"}` {
		t.Fatalf("tools = %#v", got.Tools)
	}

	if result.StopReason != "tool_use" || result.Usage.InputTokens != 31 || result.Usage.OutputTokens != 7 {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "tool_use" || result.Content[0].Name != "lookup" {
		t.Fatalf("content = %#v", result.Content)
	}
	if string(result.Content[0].Input) != `{"symbol":"Bar"}` {
		t.Fatalf("input = %s", result.Content[0].Input)
	}
}

func TestOpenAICompatCreateTextResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"final answer"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":5,"completion_tokens":2}
		}`))
	}))
	defer server.Close()

	result, err := NewOpenAICompatClient(server.URL, "key").Create(context.Background(), CreateParams{
		Model: "model", MaxTokens: 16, Messages: []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "end_turn" || len(result.Content) != 1 || result.Content[0].Text != "final answer" {
		t.Fatalf("result = %#v", result)
	}
}

func TestOpenAICompatCreateDetectsBodyError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"upstream unavailable"}}`))
	}))
	defer server.Close()

	_, err := NewOpenAICompatClient(server.URL, "key").Create(context.Background(), CreateParams{})
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Category != "transient" {
		t.Fatalf("error = %#v", err)
	}
}

func TestOpenAICompatCreateRateLimit(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("retry-after", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
	}))
	defer server.Close()

	_, err := NewOpenAICompatClient(server.URL, "key").Create(context.Background(), CreateParams{})
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Category != "rate_limited" || apiErr.RetryAfter != "3" {
		t.Fatalf("error = %#v", err)
	}
}

func TestFromOpenAIFinishReasonPreservesTruncation(t *testing.T) {
	t.Parallel()
	if got := fromOpenAIFinishReason("length"); got != "max_tokens" {
		t.Fatalf("finish reason = %q", got)
	}
}
