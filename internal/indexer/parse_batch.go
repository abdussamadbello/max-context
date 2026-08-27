package indexer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// parseSourceFiles parses independent source files on a CPU-sized worker pool.
// Results retain scan order for deterministic indexing, and every read/parse
// failure is collected deterministically. Returning any failure keeps the full
// index transaction from replacing the last known-good snapshot with a partial
// one.
func parseSourceFiles(ctx context.Context, root string, files []string) ([]*ParseResult, error) {
	parsed := make([]*ParseResult, len(files))
	parseErrs := make([]error, len(files))
	if len(files) == 0 {
		return parsed, nil
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := runtime.NumCPU()
	if workers > len(files) {
		workers = len(files)
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					continue
				}
				content, err := os.ReadFile(filepath.Join(root, files[i]))
				if err != nil {
					parseErrs[i] = fmt.Errorf("%s: read: %w", files[i], err)
					continue
				}
				res, err := ParseFile(ctx, files[i], content)
				if err != nil {
					parseErrs[i] = fmt.Errorf("%s: parse: %w", files[i], err)
					continue
				}
				parsed[i] = res
			}
		}()
	}
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := errors.Join(parseErrs...); err != nil {
		return nil, fmt.Errorf("parse source files: %w", err)
	}
	return parsed, nil
}
