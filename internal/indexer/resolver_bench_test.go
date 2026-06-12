package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// genBenchRepo writes nFiles Go files, each its own package with funcsPerFile
// functions that call within the file (so there are same-file call edges to
// resolve). nFiles*funcsPerFile is the total function count the resolver must
// index. Returns nothing; files live under root/pkgN/f.go.
func genBenchRepo(tb testing.TB, root string, nFiles, funcsPerFile int) {
	tb.Helper()
	for i := 0; i < nFiles; i++ {
		pkg := fmt.Sprintf("pkg%d", i)
		dir := filepath.Join(root, pkg)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatal(err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "package %s\n\n", pkg)
		for j := 0; j < funcsPerFile; j++ {
			// F<i>_<j> calls the next function in the same file (same-file edge).
			fmt.Fprintf(&b, "func F%d_%d() { F%d_%d() }\n", i, j, i, (j+1)%funcsPerFile)
		}
		if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte(b.String()), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
}

// BenchmarkIndexFileSingleChange measures the latency of reindexing ONE file as
// the repo grows. With the per-file rebuild of the resolver from full table
// scans, this scales with repo size (O(repo) per keystroke); the Phase 4 goal is
// to make it roughly flat across the two sizes.
func BenchmarkIndexFileSingleChange(b *testing.B) {
	const funcsPerFile = 25
	for _, nFiles := range []int{200, 2000} {
		b.Run(fmt.Sprintf("files=%d/funcs=%d", nFiles, nFiles*funcsPerFile), func(b *testing.B) {
			ctx := context.Background()
			root := b.TempDir()
			genBenchRepo(b, root, nFiles, funcsPerFile)
			database, q := openIndexDB(b, root)
			defer database.Close()
			defer q.Close()
			if err := Index(ctx, root, database, q); err != nil {
				b.Fatalf("Index: %v", err)
			}
			target := "pkg0/f.go"
			// Reuse one cache across calls (the worker's behavior) and warm it once
			// so the timed loop measures the steady-state delta cost, not the first
			// cold full scan.
			cache := NewResolverCache()
			if err := IndexFile(ctx, root, target, database, q, cache); err != nil {
				b.Fatalf("warm IndexFile: %v", err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := IndexFile(ctx, root, target, database, q, cache); err != nil {
					b.Fatalf("IndexFile: %v", err)
				}
			}
		})
	}
}
