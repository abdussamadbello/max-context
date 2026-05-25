// Package bench measures token consumption for max-context tool responses versus
// a naive/skilled Grep+Read baseline. Used by `max-context bench`.
package bench

import (
	"fmt"
	"sync"

	"github.com/pkoukk/tiktoken-go"
)

// Counter wraps a tiktoken encoder. Safe for concurrent use.
type Counter struct {
	mu  sync.Mutex
	enc *tiktoken.Tiktoken
}

// NewCounter loads the cl100k_base encoding (matches modern OpenAI/Claude approximations).
func NewCounter() (*Counter, error) {
	enc, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		return nil, fmt.Errorf("load tiktoken encoding: %w", err)
	}
	return &Counter{enc: enc}, nil
}

// Count returns the number of cl100k_base tokens in s.
func (c *Counter) Count(s string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.enc.Encode(s, nil, nil))
}
