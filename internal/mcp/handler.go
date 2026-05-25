package mcp

import (
	"encoding/json"
	"sync"
)

type ToolHandler func(args json.RawMessage) (interface{}, error)

type Handler struct {
	mu       sync.RWMutex
	handlers map[string]ToolHandler
}

func NewHandler() *Handler {
	return &Handler{handlers: make(map[string]ToolHandler)}
}

func (h *Handler) Register(name string, fn ToolHandler) {
	h.mu.Lock()
	h.handlers[name] = fn
	h.mu.Unlock()
}

func (h *Handler) Call(name string, args json.RawMessage) (result interface{}, err error) {
	h.mu.RLock()
	fn := h.handlers[name]
	h.mu.RUnlock()
	if fn == nil {
		return nil, &RPCError{Code: CodeMethodNotFound, Message: "tool not found"}
	}
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = &RPCError{Code: CodeInternalError, Message: "internal error"}
		}
	}()
	return fn(args)
}

func (h *Handler) ToolNames() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var names []string
	for n := range h.handlers {
		names = append(names, n)
	}
	return names
}
