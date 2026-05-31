package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// writeOneArgFile writes data to a temp file and returns its path. Used to pass
// large AWS CLI arguments via file:// instead of argv (avoids ARG_MAX overflow).
func writeOneArgFile(data []byte) (string, error) {
	f, err := os.CreateTemp("", "bedrock-arg-*.json")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), f.Close()
}

// writeArgFiles stages several named payloads to temp files at once, returning a
// name->path map and a single cleanup func that removes them all.
func writeArgFiles(payloads map[string][]byte) (map[string]string, func(), error) {
	paths := make(map[string]string, len(payloads))
	cleanup := func() {
		for _, p := range paths {
			os.Remove(p)
		}
	}
	for name, data := range payloads {
		p, err := writeOneArgFile(data)
		if err != nil {
			cleanup()
			return nil, func() {}, err
		}
		paths[name] = p
	}
	return paths, cleanup, nil
}

// BedrockClient calls Claude via AWS Bedrock by shelling out to the AWS CLI
// (`aws bedrock-runtime converse`). Shelling out keeps this module
// dependency-free (no AWS SDK) and reuses the CLI's SSO/profile auth + SigV4
// signing — matching the harness's "raw, auditable" philosophy.
//
// It satisfies the same caller interface as the Anthropic API Client, so the
// agent loop is backend-agnostic. Only the message/tool wire shapes differ
// (Bedrock's Converse API vs the Anthropic Messages API).
type BedrockClient struct {
	Region  string
	Profile string // AWS profile (e.g. "kargo-bedrock"); "" uses default chain
}

func NewBedrockClient(region, profile string) *BedrockClient {
	if region == "" {
		region = "us-east-1"
	}
	return &BedrockClient{Region: region, Profile: profile}
}

// --- Converse wire types (subset) ---

type bedrockReq struct {
	Messages        []bedrockMsg     `json:"messages"`
	System          []bedrockText    `json:"system,omitempty"`
	InferenceConfig bedrockInference `json:"inferenceConfig"`
	ToolConfig      *bedrockToolCfg  `json:"toolConfig,omitempty"`
}

type bedrockInference struct {
	MaxTokens   int     `json:"maxTokens"`
	Temperature float64 `json:"temperature"`
}

type bedrockText struct {
	Text string `json:"text"`
}

type bedrockToolCfg struct {
	Tools []bedrockTool `json:"tools"`
}

type bedrockTool struct {
	ToolSpec bedrockToolSpec `json:"toolSpec"`
}

type bedrockToolSpec struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

type bedrockMsg struct {
	Role    string                   `json:"role"`
	Content []map[string]interface{} `json:"content"`
}

type bedrockResp struct {
	Output struct {
		Message struct {
			Role    string                   `json:"role"`
			Content []map[string]interface{} `json:"content"`
		} `json:"message"`
	} `json:"output"`
	StopReason string `json:"stopReason"`
	Usage      struct {
		InputTokens  int `json:"inputTokens"`
		OutputTokens int `json:"outputTokens"`
	} `json:"usage"`
}

// Create issues one Converse call, translating our neutral types to/from the
// Bedrock wire format. Returns the same CreateResult the loop expects.
func (c *BedrockClient) Create(ctx context.Context, req CreateParams) (*CreateResult, error) {
	br := bedrockReq{
		InferenceConfig: bedrockInference{MaxTokens: req.MaxTokens, Temperature: req.Temperature},
		Messages:        toBedrockMessages(req.Messages),
	}
	if req.System != "" {
		br.System = []bedrockText{{Text: req.System}}
	}
	if len(req.Tools) > 0 {
		tc := &bedrockToolCfg{}
		for _, t := range req.Tools {
			var schema map[string]interface{}
			_ = json.Unmarshal(t.InputSchema, &schema)
			tc.Tools = append(tc.Tools, bedrockTool{ToolSpec: bedrockToolSpec{
				Name: t.Name, Description: t.Description,
				InputSchema: map[string]interface{}{"json": schema},
			}})
		}
		br.ToolConfig = tc
	}

	msgsJSON, _ := json.Marshal(br.Messages)
	infJSON, _ := json.Marshal(br.InferenceConfig)

	// Large arguments (the full conversation, tool schemas, system prompt) are
	// passed via `file://` temp files, NOT inline on argv. With big contexts —
	// e.g. LoCoBench repos where the agent reads sizable files — an inline
	// --messages payload overflows the OS ARG_MAX limit and fork/exec fails with
	// "argument list too long". The AWS CLI reads any argument from a file when
	// given file://<path>, so argv stays small regardless of context size.
	tmp, cleanup, err := writeArgFiles(map[string][]byte{
		"messages": msgsJSON,
	})
	if err != nil {
		return &CreateResult{}, &APIError{Category: "api_error", Message: "stage messages: " + err.Error()}
	}
	defer cleanup()

	args := []string{"bedrock-runtime", "converse",
		"--region", c.Region,
		"--model-id", req.Model,
		"--messages", "file://" + tmp["messages"],
		"--inference-config", string(infJSON), // small, fixed size — fine inline
		// The AWS CLI default read timeout (60s) is too short for large-context
		// Converse calls — LoCoBench prompts run 100k+ tokens and the model can
		// take well over a minute to first byte. Raise both timeouts generously;
		// the agent loop's own ctx deadline (10m) is the real ceiling.
		"--cli-read-timeout", "300",
		"--cli-connect-timeout", "60",
	}
	if br.System != nil {
		sysJSON, _ := json.Marshal(br.System)
		p, err := writeOneArgFile(sysJSON)
		if err != nil {
			return &CreateResult{}, &APIError{Category: "api_error", Message: "stage system: " + err.Error()}
		}
		defer os.Remove(p)
		args = append(args, "--system", "file://"+p)
	}
	if br.ToolConfig != nil {
		tcJSON, _ := json.Marshal(br.ToolConfig)
		p, err := writeOneArgFile(tcJSON)
		if err != nil {
			return &CreateResult{}, &APIError{Category: "api_error", Message: "stage tool-config: " + err.Error()}
		}
		defer os.Remove(p)
		args = append(args, "--tool-config", "file://"+p)
	}

	cmd := exec.CommandContext(ctx, "aws", args...)
	if c.Profile != "" {
		cmd.Env = append(cmd.Environ(), "AWS_PROFILE="+c.Profile)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		// Bedrock returns ThrottlingException on rate limits; map it so the loop
		// retries with backoff just like the Anthropic 429 path.
		if strings.Contains(msg, "ThrottlingException") || strings.Contains(msg, "TooManyRequests") {
			return &CreateResult{}, &APIError{Category: "rate_limited", Message: msg}
		}
		return &CreateResult{}, &APIError{Category: "api_error", Message: fmt.Sprintf("%v: %s", err, msg)}
	}

	var resp bedrockResp
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return nil, &APIError{Category: "decode", Message: err.Error()}
	}
	return &CreateResult{
		Content:    fromBedrockContent(resp.Output.Message.Content),
		StopReason: normalizeStop(resp.StopReason),
		Usage:      Usage{InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens},
		StatusCode: 200,
	}, nil
}

// toBedrockMessages converts neutral messages to Converse content blocks.
func toBedrockMessages(msgs []Message) []bedrockMsg {
	out := make([]bedrockMsg, 0, len(msgs))
	for _, m := range msgs {
		bm := bedrockMsg{Role: m.Role}
		for _, b := range m.Content {
			switch b.Type {
			case "text":
				bm.Content = append(bm.Content, map[string]interface{}{"text": b.Text})
			case "tool_use":
				var input interface{}
				_ = json.Unmarshal(b.Input, &input)
				bm.Content = append(bm.Content, map[string]interface{}{
					"toolUse": map[string]interface{}{
						"toolUseId": b.ID, "name": b.Name, "input": input,
					},
				})
			case "tool_result":
				bm.Content = append(bm.Content, map[string]interface{}{
					"toolResult": map[string]interface{}{
						"toolUseId": b.ToolUseID,
						"content":   []map[string]interface{}{{"text": b.Content}},
					},
				})
			}
		}
		out = append(out, bm)
	}
	return out
}

// fromBedrockContent converts Converse response content to neutral blocks.
func fromBedrockContent(content []map[string]interface{}) []ContentBlock {
	var out []ContentBlock
	for _, c := range content {
		if txt, ok := c["text"].(string); ok {
			out = append(out, ContentBlock{Type: "text", Text: txt})
			continue
		}
		if tu, ok := c["toolUse"].(map[string]interface{}); ok {
			input, _ := json.Marshal(tu["input"])
			id, _ := tu["toolUseId"].(string)
			name, _ := tu["name"].(string)
			out = append(out, ContentBlock{Type: "tool_use", ID: id, Name: name, Input: input})
		}
	}
	return out
}

// normalizeStop maps Bedrock stop reasons to the Anthropic ones the loop checks.
func normalizeStop(s string) string {
	switch s {
	case "tool_use":
		return "tool_use"
	case "end_turn", "stop_sequence", "max_tokens":
		return "end_turn"
	default:
		return s
	}
}
