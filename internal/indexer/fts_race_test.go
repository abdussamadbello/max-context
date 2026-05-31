package indexer

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
)

// TestIncrementalReindexNoFTSWindow is the production-hot-path regression gate for
// the E01 FTS race. The file watcher reindexes ONE changed file at a time via
// IndexFile; with the V6 sync triggers each per-file update commits its FTS rows
// atomically, so a symbol in an UNCHANGED file must remain findable at all times.
// (Pre-V6, the post-commit full FTS rebuild blanked the whole index on every edit.)
func TestIncrementalReindexNoFTSWindow(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root, "a.go", "package p\n\nfunc AlphaHandler() {}\n")
	writeFile(t, root, "b.go", "package p\n\nfunc BetaHandler() {}\nfunc GammaHandler() {}\n")

	database, err := db.Open(filepath.Join(root, ".max-context", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if err := Index(ctx, root, database, q); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var emptyReads, totalReads int64

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// BetaHandler lives in b.go, which is never touched — it must ALWAYS
			// be present in the FTS while a.go is reindexed.
			var n int
			if database.QueryRow(
				"SELECT COUNT(*) FROM functions_fts WHERE functions_fts MATCH ?", "BetaHandler",
			).Scan(&n) == nil {
				atomic.AddInt64(&totalReads, 1)
				if n == 0 {
					atomic.AddInt64(&emptyReads, 1)
				}
			}
		}
	}()

	for i := 0; i < 50; i++ {
		if err := IndexFile(ctx, root, "a.go", database, q); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("IndexFile %d: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()

	if totalReads == 0 {
		t.Fatal("reader made no reads")
	}
	if emptyReads != 0 {
		t.Errorf("FTS empty window on incremental reindex: BetaHandler (unchanged file) missing in %d/%d reads — triggers not maintaining FTS atomically", emptyReads, totalReads)
	}
	t.Logf("incremental hot path: %d reads, BetaHandler never missing (0 empty windows)", totalReads)
}
