// Package bench measures token consumption for max-context tool responses versus
// a naive/skilled Grep+Read baseline. Used by `max-context bench`.
package bench

import "github.com/maxcontext/max-context/internal/contextpack"

// Counter is retained as a compatibility alias for the benchmark package.
// Product code and benchmarks now share the same compiled-in tokenizer.
type Counter = contextpack.Counter

// NewCounter loads the compiled-in cl100k_base encoding. The vocabulary lives
// in the Go module, so tests and benchmark runs never depend on a first-run
// network download or a mutable machine cache.
func NewCounter() (*Counter, error) {
	return contextpack.NewCounter()
}
