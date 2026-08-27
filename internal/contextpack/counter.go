// Package contextpack builds deterministic, token-budgeted evidence payloads.
package contextpack

import (
	"encoding/json"
	"sync"

	"github.com/tiktoken-go/tokenizer/codec"
)

// Counter wraps the compiled-in cl100k_base encoder and is safe for concurrent
// use. cl100k_base is the initial budget profile; callers should name it in
// responses rather than implying that it exactly matches every agent model.
type Counter struct {
	mu  sync.Mutex
	enc *codec.Codec
}

func NewCounter() (*Counter, error) {
	return &Counter{enc: codec.NewCl100kBase()}, nil
}

func (c *Counter) Count(s string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids, _, err := c.enc.Encode(s)
	if err != nil {
		return 0, err
	}
	return len(ids), nil
}

func (c *Counter) CountJSON(value interface{}) (int, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	return c.Count(string(b))
}
