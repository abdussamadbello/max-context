package indexer

import (
	"context"
	"database/sql"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
)

// repoRoot returns the max-context-core repo root, derived from this test
// file's location (internal/indexer/ -> ../..).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

// indexSelfRepo builds a fresh index of the self-repo into a temp DB and
// returns the open handle. Shared by the baseline (Step 1) and the
// precision-win measurement (Step 5).
func indexSelfRepo(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "self.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		t.Fatalf("PrepareQueries: %v", err)
	}
	t.Cleanup(func() { _ = q.Close() })
	if err := Index(ctx, repoRoot(t), database, q); err != nil {
		t.Fatalf("Index: %v", err)
	}
	return database
}

// TestResolutionBaseline indexes the self-repo and logs the call-edge
// resolution distribution. It is INSTRUMENTATION, not a gate: it always passes
// and exists to make today's precision visible (measure-first). Run with
// `go test ./internal/indexer/ -run TestResolutionBaseline -v` to see the table.
func TestResolutionBaseline(t *testing.T) {
	database := indexSelfRepo(t)

	var total int
	if err := database.QueryRow("SELECT COUNT(*) FROM calls").Scan(&total); err != nil {
		t.Fatalf("count calls: %v", err)
	}
	hist, err := db.ResolutionHistogram(database)
	if err != nil {
		t.Fatalf("ResolutionHistogram: %v", err)
	}

	markers := make([]string, 0, len(hist))
	for m := range hist {
		markers = append(markers, m)
	}
	sort.Slice(markers, func(i, j int) bool { return hist[markers[i]] > hist[markers[j]] })

	t.Logf("call-edge resolution distribution (total=%d edges):", total)
	for _, m := range markers {
		pct := 0.0
		if total > 0 {
			pct = 100 * float64(hist[m]) / float64(total)
		}
		t.Logf("  %-16s %6d  (%.1f%%)", m, hist[m], pct)
	}
	if total == 0 {
		t.Fatal("expected the self-repo index to contain call edges")
	}

	// Regression gate (Go edges): the precision layer must keep resolving the
	// bulk of Go call edges. Thresholds are deliberately slack so normal code
	// churn doesn't trip them — they catch a real regression (e.g. the resolver
	// silently falling back to name-global), not minor drift.
	goHist, err := goResolutionHistogram(database)
	if err != nil {
		t.Fatalf("goResolutionHistogram: %v", err)
	}
	goTotal := 0
	for _, n := range goHist {
		goTotal += n
	}
	if goTotal == 0 {
		t.Fatal("expected Go call edges in the self-repo index")
	}
	// No Go edge should use the legacy name-global fallback — Go always takes a
	// precise marker or an honest unresolved/builtin/import-qualified.
	if goHist["name-global"] != 0 {
		t.Errorf("Go edges resolved as name-global: %d (want 0 — the resolver should never fall back for Go)", goHist["name-global"])
	}
	// A majority of Go edges should be precisely linked or correctly classified
	// (anything other than the deterministic-ceiling 'unresolved').
	resolved := goTotal - goHist["unresolved"]
	if ratio := float64(resolved) / float64(goTotal); ratio < 0.40 {
		t.Errorf("Go precise-resolution ratio = %.2f (want >= 0.40); histogram=%v", ratio, goHist)
	}
}

// goResolutionHistogram counts call edges by resolution, restricted to edges
// whose caller is a Go function.
func goResolutionHistogram(database *sql.DB) (map[string]int, error) {
	rows, err := database.Query(`
		SELECT e.resolution, COUNT(*)
		FROM calls e JOIN functions f ON f.id = e.caller_id
		WHERE f.language = 'go'
		GROUP BY e.resolution`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var m string
		var n int
		if err := rows.Scan(&m, &n); err != nil {
			return nil, err
		}
		out[m] = n
	}
	return out, rows.Err()
}
