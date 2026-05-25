package mcp

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"os"
)

type Server struct {
	handler     *Handler
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	schemas     []ToolSchema
	projectRoot string // Phase 6: for resources/read (summary.md, architecture.md)
}

func NewServer(handler *Handler, toolSchemas []ToolSchema) *Server {
	return &Server{
		handler: handler,
		stdin:   os.Stdin,
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		schemas: toolSchemas,
	}
}

// SetProjectRoot sets the project root for MCP resources (Phase 6). Call before Serve.
func (s *Server) SetProjectRoot(root string) { s.projectRoot = root }

func (s *Server) ToolSchemas() []ToolSchema {
	return s.schemas
}

func (s *Server) Serve() error {
	sc := bufio.NewScanner(s.stdin)
	enc := json.NewEncoder(s.stdout)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(JSONRPCResponse{JSONRPC: "2.0", ID: nil, Error: &RPCError{Code: CodeParseError, Message: err.Error()}})
			continue
		}
		resp := s.handleMethod(&req)
		if resp == nil {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			log.Printf("encode response: %v", err)
			continue
		}
	}
	return sc.Err()
}
