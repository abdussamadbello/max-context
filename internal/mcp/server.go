package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
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

// maxMessageBytes caps a single newline-delimited JSON-RPC message. bufio.Scanner
// defaults to 64 KiB and returns ErrTooLong past it, which killed the whole server
// mid-session — a large tools/call (e.g. get_impact with an explicit file list) is
// well within what a client may legitimately send.
const maxMessageBytes = 16 << 20

func (s *Server) Serve() error {
	sc := bufio.NewScanner(s.stdin)
	sc.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)
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
	if err := sc.Err(); err != nil {
		// The stream is unrecoverable (we cannot resync mid-message), but tell the
		// client why instead of exiting silently.
		_ = enc.Encode(JSONRPCResponse{JSONRPC: "2.0", ID: nil, Error: &RPCError{
			Code:    CodeParseError,
			Message: fmt.Sprintf("stdio read failed, server stopping: %v", err),
		}})
		return err
	}
	return nil
}
