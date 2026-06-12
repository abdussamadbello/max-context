package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/artifacts"
)

// TestRunWorkerRecordsIndexFailure proves the worker no longer swallows
// IndexFile errors: a path that fails to index (here a directory, whose read
// errors with a non-NotExist error) is recorded in index_errors and surfaces
// via ReadIndexHealth, instead of vanishing behind `_ = IndexFile(...)`.
func TestRunWorkerRecordsIndexFailure(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	// A directory at the indexed path makes os.ReadFile fail with "is a
	// directory" (not NotExist), so IndexFile returns an error the worker records.
	if err := os.MkdirAll(filepath.Join(root, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	database, q := openIndexDB(t, root)
	defer database.Close()
	defer q.Close()

	ch := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		RunWorker(ctx, root, database, q, ch)
		close(done)
	}()
	ch <- "subdir"
	close(ch) // worker drains the queued path, then returns on the closed channel
	<-done

	h := artifacts.ReadIndexHealth(database)
	if h.FailedFiles != 1 {
		t.Fatalf("FailedFiles = %d, want 1 (worker swallowed the error?)", h.FailedFiles)
	}
	if h.Healthy {
		t.Errorf("Healthy = true, want false with a recorded failure")
	}

	var fp, msg string
	if err := database.QueryRow(`SELECT file_path, error FROM index_errors`).Scan(&fp, &msg); err != nil {
		t.Fatalf("read index_errors: %v", err)
	}
	if fp != "subdir" {
		t.Errorf("recorded file_path = %q, want %q", fp, "subdir")
	}
	if msg == "" {
		t.Errorf("recorded error message is empty")
	}
}
