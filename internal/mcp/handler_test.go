package mcp

import (
	"encoding/json"
	"testing"
)

func TestHandler_Call(t *testing.T) {
	h := NewHandler()
	h.Register("echo", func(args json.RawMessage) (interface{}, error) {
		return []ContentItem{{Type: "text", Text: string(args)}}, nil
	})
	result, err := h.Call("echo", json.RawMessage(`"hi"`))
	if err != nil {
		t.Fatal(err)
	}
	content, ok := result.([]ContentItem)
	if !ok || len(content) != 1 || content[0].Text != `"hi"` {
		t.Fatalf("unexpected result: %v", result)
	}
}

func TestHandler_CallNotFound(t *testing.T) {
	h := NewHandler()
	_, err := h.Call("missing", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if rpcErr, ok := err.(*RPCError); !ok || rpcErr.Code != CodeMethodNotFound {
		t.Fatalf("expected RPCError MethodNotFound: %v", err)
	}
}
