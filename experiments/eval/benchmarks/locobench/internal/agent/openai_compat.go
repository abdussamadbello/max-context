package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultOpenAICompatBaseURL = "https://opencode.ai/zen/v1"

// OpenAICompatClient calls an OpenAI-compatible Chat Completions endpoint and
// translates its wire format into the evaluator's backend-neutral types.
// OpenCode Zen is the default endpoint, while BaseURL remains configurable so
// the harness does not couple its experiment logic to one provider.
type OpenAICompatClient struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
}

// NewOpenAICompatClient returns a client for an OpenAI-compatible API. An empty
// baseURL selects OpenCode Zen. The key is retained in memory only and is never
// included in errors or run artifacts.
func NewOpenAICompatClient(baseURL, apiKey string) *OpenAICompatClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultOpenAICompatBaseURL
	}
	return &OpenAICompatClient{
		APIKey:  apiKey,
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 5 * time.Minute},
	}
}

type openAIRequest struct {
	Model       string          `json:"model"`
	MaxTokens   int             `json:"max_tokens"`
	Temperature float64         `json:"temperature"`
	Messages    []openAIMessage `json:"messages"`
	Tools       []openAITool    `json:"tools,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    *string          `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Arguments   string          `json:"arguments,omitempty"`
}

type openAIToolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content   json.RawMessage  `json:"content"`
			ToolCalls []openAIToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *apiError `json:"error,omitempty"`
}

// Create implements Caller using the OpenAI Chat Completions protocol.
func (c *OpenAICompatClient) Create(ctx context.Context, req CreateParams) (*CreateResult, error) {
	messages, err := toOpenAIMessages(req.System, req.Messages)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(openAIRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Messages:    messages,
		Tools:       toOpenAITools(req.Tools),
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, &APIError{Category: "network", Message: err.Error()}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusTooManyRequests {
		return &CreateResult{StatusCode: resp.StatusCode}, &APIError{
			Category: "rate_limited", Message: string(respBody), RetryAfter: resp.Header.Get("retry-after"),
		}
	}
	if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusConflict ||
		resp.StatusCode == http.StatusTooEarly || resp.StatusCode >= http.StatusInternalServerError {
		return &CreateResult{StatusCode: resp.StatusCode}, &APIError{
			Category: "transient", Message: fmt.Sprintf("status %d: %s", resp.StatusCode, string(respBody)),
			RetryAfter: resp.Header.Get("retry-after"),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return &CreateResult{StatusCode: resp.StatusCode}, &APIError{
			Category: "api_error", Message: fmt.Sprintf("status %d: %s", resp.StatusCode, string(respBody)),
		}
	}

	var wire openAIResponse
	if err := json.Unmarshal(respBody, &wire); err != nil {
		return nil, &APIError{Category: "decode", Message: err.Error()}
	}
	if wire.Error != nil {
		if wire.Error.Type == "server_error" {
			return &CreateResult{StatusCode: resp.StatusCode}, &APIError{Category: "transient", Message: wire.Error.Message}
		}
		return &CreateResult{StatusCode: resp.StatusCode}, &APIError{Category: "api_error", Message: wire.Error.Message}
	}
	if len(wire.Choices) == 0 {
		return &CreateResult{StatusCode: resp.StatusCode}, &APIError{Category: "decode", Message: "response contained no choices"}
	}

	choice := wire.Choices[0]
	content, err := fromOpenAIMessage(choice.Message.Content, choice.Message.ToolCalls)
	if err != nil {
		return nil, &APIError{Category: "decode", Message: err.Error()}
	}
	return &CreateResult{
		Content:    content,
		StopReason: fromOpenAIFinishReason(choice.FinishReason),
		Usage: Usage{
			InputTokens:  wire.Usage.PromptTokens,
			OutputTokens: wire.Usage.CompletionTokens,
		},
		StatusCode: resp.StatusCode,
	}, nil
}

func toOpenAITools(tools []Tool) []openAITool {
	out := make([]openAITool, 0, len(tools))
	for _, tool := range tools {
		out = append(out, openAITool{Type: "function", Function: openAIFunction{
			Name: tool.Name, Description: tool.Description, Parameters: tool.InputSchema,
		}})
	}
	return out
}

// toOpenAIMessages expands Anthropic-style tool_result blocks into the separate
// role=tool messages required by Chat Completions.
func toOpenAIMessages(system string, messages []Message) ([]openAIMessage, error) {
	out := make([]openAIMessage, 0, len(messages)+1)
	if system != "" {
		text := system
		out = append(out, openAIMessage{Role: "system", Content: &text})
	}
	for _, msg := range messages {
		switch msg.Role {
		case "assistant":
			var texts []string
			var calls []openAIToolCall
			for _, block := range msg.Content {
				switch block.Type {
				case "text":
					texts = append(texts, block.Text)
				case "tool_use":
					args := string(block.Input)
					if args == "" || args == "null" {
						args = "{}"
					}
					calls = append(calls, openAIToolCall{ID: block.ID, Type: "function", Function: openAIFunction{
						Name: block.Name, Arguments: args,
					}})
				}
			}
			m := openAIMessage{Role: "assistant", ToolCalls: calls}
			if len(texts) > 0 {
				text := strings.Join(texts, "\n")
				m.Content = &text
			}
			out = append(out, m)

		case "user":
			var texts []string
			for _, block := range msg.Content {
				switch block.Type {
				case "text":
					texts = append(texts, block.Text)
				case "tool_result":
					if len(texts) > 0 {
						text := strings.Join(texts, "\n")
						out = append(out, openAIMessage{Role: "user", Content: &text})
						texts = nil
					}
					text := block.Content
					if block.IsError {
						text = "ERROR: " + text
					}
					out = append(out, openAIMessage{Role: "tool", ToolCallID: block.ToolUseID, Content: &text})
				}
			}
			if len(texts) > 0 {
				text := strings.Join(texts, "\n")
				out = append(out, openAIMessage{Role: "user", Content: &text})
			}

		default:
			return nil, fmt.Errorf("unsupported message role %q", msg.Role)
		}
	}
	return out, nil
}

func fromOpenAIMessage(raw json.RawMessage, calls []openAIToolCall) ([]ContentBlock, error) {
	var out []ContentBlock
	if len(raw) > 0 && string(raw) != "null" {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, fmt.Errorf("decode message content: %w", err)
		}
		if text != "" {
			out = append(out, ContentBlock{Type: "text", Text: text})
		}
	}
	for _, call := range calls {
		input := json.RawMessage(call.Function.Arguments)
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		if !json.Valid(input) {
			return nil, fmt.Errorf("tool %q returned invalid JSON arguments", call.Function.Name)
		}
		out = append(out, ContentBlock{
			Type: "tool_use", ID: call.ID, Name: call.Function.Name, Input: input,
		})
	}
	return out, nil
}

func fromOpenAIFinishReason(reason string) string {
	switch reason {
	case "tool_calls", "function_call":
		return "tool_use"
	case "length":
		return "max_tokens"
	default:
		return "end_turn"
	}
}
