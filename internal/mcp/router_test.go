package mcp

import (
	"encoding/json"
	"errors"
	"testing"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	h := NewHandler()
	h.Register("ok_tool", func(args json.RawMessage) (interface{}, error) {
		return []ContentItem{{Type: "text", Text: `{"hello":"world"}`}}, nil
	})
	h.Register("invalid_params_tool", func(args json.RawMessage) (interface{}, error) {
		return nil, &RPCError{Code: CodeInvalidParams, Message: "query required"}
	})
	h.Register("boom_tool", func(args json.RawMessage) (interface{}, error) {
		return nil, errors.New("disk on fire")
	})
	annotations := &ToolAnnotations{Title: "OK Tool"}
	ro := true
	annotations.ReadOnlyHint = &ro
	schemas := []ToolSchema{{
		Name:        "ok_tool",
		Title:       "OK Tool",
		Description: "test tool",
		InputSchema: map[string]interface{}{"type": "object"},
		Annotations: annotations,
	}}
	return NewServer(h, schemas)
}

func initialize(t *testing.T, s *Server, params string) InitializeResult {
	t.Helper()
	req := &JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: json.RawMessage(params)}
	resp := s.handleMethod(req)
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}
	res, ok := resp.Result.(InitializeResult)
	if !ok {
		t.Fatalf("unexpected result type %T", resp.Result)
	}
	return res
}

func TestInitializeVersionNegotiation(t *testing.T) {
	s := testServer(t)

	// Supported requested version is echoed verbatim (bench clients pin 2024-11-05).
	for _, v := range SupportedProtocolVersions {
		if got := initialize(t, s, `{"protocolVersion":"`+v+`"}`).ProtocolVersion; got != v {
			t.Errorf("requested %s, got %s", v, got)
		}
	}

	// Unsupported version -> newest supported.
	if got := initialize(t, s, `{"protocolVersion":"2099-01-01"}`).ProtocolVersion; got != SupportedProtocolVersions[0] {
		t.Errorf("unsupported version negotiation = %s, want %s", got, SupportedProtocolVersions[0])
	}

	// Absent params -> newest supported.
	if got := initialize(t, s, ``).ProtocolVersion; got != SupportedProtocolVersions[0] {
		t.Errorf("absent params negotiation = %s, want %s", got, SupportedProtocolVersions[0])
	}
}

func TestToolsListIncludesAnnotations(t *testing.T) {
	s := testServer(t)
	resp := s.handleMethod(&JSONRPCRequest{JSONRPC: "2.0", ID: 2, Method: "tools/list"})
	body, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Tools []struct {
			Name        string `json:"name"`
			Title       string `json:"title"`
			Annotations *struct {
				ReadOnlyHint *bool `json:"readOnlyHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Tools) != 1 || out.Tools[0].Title != "OK Tool" {
		t.Fatalf("tools/list = %s", body)
	}
	if out.Tools[0].Annotations == nil || out.Tools[0].Annotations.ReadOnlyHint == nil || !*out.Tools[0].Annotations.ReadOnlyHint {
		t.Errorf("annotations missing readOnlyHint: %s", body)
	}
}

func callTool(t *testing.T, s *Server, name string) *JSONRPCResponse {
	t.Helper()
	params, _ := json.Marshal(ToolsCallParams{Name: name, Arguments: json.RawMessage(`{}`)})
	return s.handleMethod(&JSONRPCRequest{JSONRPC: "2.0", ID: 3, Method: "tools/call", Params: params})
}

func TestToolsCallErrorsBecomeIsErrorResults(t *testing.T) {
	s := testServer(t)

	for _, name := range []string{"invalid_params_tool", "boom_tool"} {
		resp := callTool(t, s, name)
		if resp.Error != nil {
			t.Fatalf("%s: handler error must not be a protocol error, got %+v", name, resp.Error)
		}
		res, ok := resp.Result.(ToolsCallResult)
		if !ok {
			t.Fatalf("%s: unexpected result type %T", name, resp.Result)
		}
		if !res.IsError {
			t.Errorf("%s: isError = false, want true", name)
		}
		if len(res.Content) != 1 || res.Content[0].Type != "text" {
			t.Fatalf("%s: content = %+v", name, res.Content)
		}
		var payload struct {
			Error struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(res.Content[0].Text), &payload); err != nil {
			t.Fatalf("%s: error text is not JSON: %q", name, res.Content[0].Text)
		}
		if payload.Error.Code == 0 || payload.Error.Message == "" {
			t.Errorf("%s: error payload incomplete: %q", name, res.Content[0].Text)
		}
	}

	// A successful call carries no isError.
	res := callTool(t, s, "ok_tool").Result.(ToolsCallResult)
	if res.IsError {
		t.Error("successful call must not set isError")
	}
}

func TestToolsCallUnknownToolStaysProtocolError(t *testing.T) {
	s := testServer(t)
	resp := callTool(t, s, "no_such_tool")
	if resp.Error == nil || resp.Error.Code != CodeMethodNotFound {
		t.Fatalf("unknown tool must stay a -32601 protocol error, got %+v", resp)
	}
}
