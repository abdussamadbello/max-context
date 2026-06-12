package mcp

import "encoding/json"

const ProtocolVersion = "2024-11-05"
const ServerName = "max-context"
const ServerVersion = "0.1.0"

// SupportedProtocolVersions, newest first. The server echoes the client's
// requested version when supported, otherwise answers with the newest one
// (the negotiation rule from the MCP spec). Nothing in this server's feature
// set (tools + resources over stdio) differs across these revisions.
var SupportedProtocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const CodeParseError = -32700
const CodeMethodNotFound = -32601
const CodeInvalidParams = -32602
const CodeInternalError = -32603
const CodeIndexNotReady = -32001
const CodeResourceNotFound = -32002
const CodeIndexBusy = -32003   // index data present but FTS transiently unavailable (rebuilding/locked) — retry, don't reindex
const CodeQuerySyntax = -32004 // the search query was not valid FTS5 syntax — simplify it, don't retry verbatim or reindex

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return e.Message }

type InitializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	Capabilities    struct {
		Tools struct {
			ListChanged bool `json:"listChanged"`
		} `json:"tools"`
		Resources *struct {
			Subscribe   bool `json:"subscribe,omitempty"`
			ListChanged bool `json:"listChanged,omitempty"`
		} `json:"resources,omitempty"`
	} `json:"capabilities"`
	ServerInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

// Resource (Phase 6): listed in resources/list.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type ResourcesListResult struct {
	Resources  []Resource `json:"resources"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

type ResourcesReadParams struct {
	URI string `json:"uri"`
}

type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

type ResourcesReadResult struct {
	Contents []ResourceContent `json:"contents"`
}

type ToolsListResult struct {
	Tools []ToolSchema `json:"tools"`
}

// ToolAnnotations are the spec's behavior hints (2025-03-26+). Pointer bools
// because the spec defaults are asymmetric (destructiveHint and openWorldHint
// default to true when absent).
type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

type ToolSchema struct {
	Name         string           `json:"name"`
	Title        string           `json:"title,omitempty"`
	Description  string           `json:"description"`
	InputSchema  interface{}      `json:"inputSchema"`
	OutputSchema interface{}      `json:"outputSchema,omitempty"`
	Annotations  *ToolAnnotations `json:"annotations,omitempty"`
}

type ToolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type ToolsCallResult struct {
	Content []ContentItem `json:"content"`
	// IsError marks a tool-execution failure (bad params, index not ready,
	// query syntax) so the model can read the message and self-correct —
	// per spec these are NOT protocol-level errors.
	IsError bool `json:"isError,omitempty"`
}

// InitializeParams is the subset of the client's initialize params the server
// negotiates on.
type InitializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
