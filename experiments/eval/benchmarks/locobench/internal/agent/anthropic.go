// Package agent provides a minimal, auditable Anthropic Messages API client and
// a generic tool-use agent loop. It is intentionally self-contained (raw HTTP,
// no SDK) so a reviewer can inspect every byte sent to the model — matching the
// experiment's "audit me, don't trust me" credibility goal.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const anthropicURL = "https://api.anthropic.com/v1/messages"
const anthropicVersion = "2023-06-01"

// Client is a thin Anthropic Messages API client.
type Client struct {
	APIKey string
	HTTP   *http.Client
}

// NewClient builds a client with a generous timeout (agent turns can be slow).
func NewClient(apiKey string) *Client {
	return &Client{APIKey: apiKey, HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

// --- Wire types (subset of the Messages API we use) ---

// Tool is a tool definition advertised to the model.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Message is one conversation turn. Content is a slice of content blocks.
type Message struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// ContentBlock is a polymorphic content item: text, tool_use, or tool_result.
type ContentBlock struct {
	Type string `json:"type"`

	// type=text
	Text string `json:"text,omitempty"`

	// type=tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// type=tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type messagesRequest struct {
	Model       string    `json:"model"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature float64   `json:"temperature"`
	System      string    `json:"system,omitempty"`
	Tools       []Tool    `json:"tools,omitempty"`
	Messages    []Message `json:"messages"`
}

// Usage is the token accounting returned by the API (real, not estimated).
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type messagesResponse struct {
	ID         string         `json:"id"`
	Role       string         `json:"role"`
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      Usage          `json:"usage"`
	Error      *apiError      `json:"error,omitempty"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// CreateResult is one model response plus its real token usage.
type CreateResult struct {
	Content    []ContentBlock
	StopReason string
	Usage      Usage
	StatusCode int
}

// Create issues one Messages API call. It returns a typed error category so the
// agent loop can record rate limits vs hard errors distinctly (no silent drops).
func (c *Client) Create(ctx context.Context, req CreateParams) (*CreateResult, error) {
	body, err := json.Marshal(messagesRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		System:      req.System,
		Tools:       req.Tools,
		Messages:    req.Messages,
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", c.APIKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

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
	if resp.StatusCode != http.StatusOK {
		return &CreateResult{StatusCode: resp.StatusCode}, &APIError{
			Category: "api_error", Message: fmt.Sprintf("status %d: %s", resp.StatusCode, string(respBody)),
		}
	}

	var mr messagesResponse
	if err := json.Unmarshal(respBody, &mr); err != nil {
		return nil, &APIError{Category: "decode", Message: err.Error()}
	}
	if mr.Error != nil {
		return &CreateResult{StatusCode: resp.StatusCode}, &APIError{Category: "api_error", Message: mr.Error.Message}
	}
	return &CreateResult{
		Content:    mr.Content,
		StopReason: mr.StopReason,
		Usage:      mr.Usage,
		StatusCode: resp.StatusCode,
	}, nil
}

// CreateParams are the inputs to one Messages call.
type CreateParams struct {
	Model       string
	MaxTokens   int
	Temperature float64
	System      string
	Tools       []Tool
	Messages    []Message
}

// APIError categorizes failures so the run log can distinguish rate limits,
// network faults, and hard API errors rather than collapsing them.
type APIError struct {
	Category   string // "rate_limited" | "api_error" | "network" | "decode"
	Message    string
	RetryAfter string
}

func (e *APIError) Error() string { return e.Category + ": " + e.Message }
