package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func (s *Server) handleMethod(req *JSONRPCRequest) *JSONRPCResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req.ID)
	case "initialized":
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: nil}
	case "tools/list":
		return s.handleToolsList(req.ID)
	case "tools/call":
		return s.handleToolsCall(req.ID, req.Params)
	case "resources/list":
		return s.handleResourcesList(req.ID, req.Params)
	case "resources/read":
		return s.handleResourcesRead(req.ID, req.Params)
	case "notifications/cancelled":
		return nil
	case "ping":
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{}}
	default:
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &RPCError{Code: CodeMethodNotFound, Message: "method not found: " + req.Method}}
	}
}

func (s *Server) handleInitialize(id interface{}) *JSONRPCResponse {
	res := InitializeResult{}
	res.ProtocolVersion = ProtocolVersion
	res.Capabilities.Tools.ListChanged = false
	res.Capabilities.Resources = &struct {
		Subscribe   bool `json:"subscribe,omitempty"`
		ListChanged bool `json:"listChanged,omitempty"`
	}{}
	res.ServerInfo.Name = ServerName
	res.ServerInfo.Version = ServerVersion
	return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Result: res}
}

func (s *Server) handleToolsList(id interface{}) *JSONRPCResponse {
	tools := s.ToolSchemas()
	return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Result: ToolsListResult{Tools: tools}}
}

func (s *Server) handleToolsCall(id interface{}, params json.RawMessage) *JSONRPCResponse {
	var p ToolsCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: CodeInvalidParams, Message: err.Error()}}
	}
	if p.Name == "" {
		return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: CodeInvalidParams, Message: "missing name"}}
	}
	result, err := s.handler.Call(p.Name, p.Arguments)
	if err != nil {
		if rpcErr, ok := err.(*RPCError); ok {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: rpcErr}
		}
		return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: CodeInternalError, Message: fmt.Sprintf("%v", err)}}
	}
	content, _ := result.([]ContentItem)
	return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Result: ToolsCallResult{Content: content}}
}

func (s *Server) handleResourcesList(id interface{}, params json.RawMessage) *JSONRPCResponse {
	// Phase 6: expose summary.md and architecture.md as MCP resources (text/markdown)
	resources := []Resource{
		{URI: "maxcontext://project/summary", Name: "Project summary", Description: "Pre-computed project summary (<50 lines)", MimeType: "text/markdown"},
		{URI: "maxcontext://project/architecture", Name: "Architecture", Description: "Tech stack, directory map, entry points, module dependencies", MimeType: "text/markdown"},
	}
	return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Result: ResourcesListResult{Resources: resources}}
}

func (s *Server) handleResourcesRead(id interface{}, params json.RawMessage) *JSONRPCResponse {
	var p ResourcesReadParams
	if err := json.Unmarshal(params, &p); err != nil || p.URI == "" {
		return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: CodeInvalidParams, Message: "missing uri"}}
	}
	dir := s.projectRoot
	if dir == "" {
		return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: CodeInternalError, Message: "project root not set"}}
	}
	var text string
	switch p.URI {
	case "maxcontext://project/summary":
		summary, err := readFile(dir, ".max-context", "summary.md")
		if err != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: CodeResourceNotFound, Message: "summary not found", Data: p.URI}}
		}
		text = summary
	case "maxcontext://project/architecture":
		arch, err := readFile(dir, ".max-context", "architecture.md")
		if err != nil {
			return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: CodeResourceNotFound, Message: "architecture not found", Data: p.URI}}
		}
		text = arch
	default:
		return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: CodeResourceNotFound, Message: "unknown resource", Data: p.URI}}
	}
	return &JSONRPCResponse{JSONRPC: "2.0", ID: id, Result: ResourcesReadResult{
		Contents: []ResourceContent{{URI: p.URI, MimeType: "text/markdown", Text: text}},
	}}
}

func readFile(root, subdir, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, subdir, name))
	return string(b), err
}
