package indexer

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
)

// indexFixture indexes a committed fixture directory (relative to the repo root)
// and returns the open DB. Used by the TS/Python resolution gates so they run
// against real, reviewable code rather than inline strings.
func indexFixture(t *testing.T, relDir string) *sql.DB {
	t.Helper()
	root := filepath.Join(repoRoot(t), relDir)
	database, err := db.Open(filepath.Join(t.TempDir(), "fix.db"))
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
	if err := Index(context.Background(), root, database, q); err != nil {
		t.Fatalf("Index %s: %v", relDir, err)
	}
	return database
}

// langResolutionHistogram counts call edges by resolution, restricted to a
// caller language. Generalizes goResolutionHistogram for any modeled language.
func langResolutionHistogram(t *testing.T, database *sql.DB, lang string) map[string]int {
	t.Helper()
	rows, err := database.Query(`
		SELECT e.resolution, COUNT(*)
		FROM calls e JOIN functions f ON f.id = e.caller_id
		WHERE f.language = ?
		GROUP BY e.resolution`, lang)
	if err != nil {
		t.Fatalf("hist(%s): %v", lang, err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var m string
		var n int
		if err := rows.Scan(&m, &n); err != nil {
			t.Fatal(err)
		}
		out[m] = n
	}
	return out
}

// assertModeledLangGate enforces the shared contract for a modeled language:
// (1) edges exist, (2) zero name-global (the language is modeled, not a guess),
// (3) a meaningful share is precisely linked or honestly classified.
func assertModeledLangGate(t *testing.T, hist map[string]int, lang string, minPreciseRatio float64) {
	t.Helper()
	total := 0
	for _, n := range hist {
		total += n
	}
	if total == 0 {
		t.Fatalf("%s: no call edges indexed in fixture", lang)
	}
	if hist["name-global"] != 0 {
		t.Errorf("%s: %d edges resolved name-global (want 0 — modeled language must not fall back)", lang, hist["name-global"])
	}
	resolved := total - hist["unresolved"]
	if ratio := float64(resolved) / float64(total); ratio < minPreciseRatio {
		t.Errorf("%s: precise/honest ratio %.2f < %.2f; hist=%v", lang, ratio, minPreciseRatio, hist)
	}
	t.Logf("%s fixture resolution (n=%d): %v", lang, total, hist)
}

// TestTypeScriptFixtureGate is the TS regression gate against testdata/ts_fixture.
func TestTypeScriptFixtureGate(t *testing.T) {
	database := indexFixture(t, "testdata/ts_fixture")
	hist := langResolutionHistogram(t, database, "typescript")
	// TS annotations are explicit, so expect a high precise/honest share.
	assertModeledLangGate(t, hist, "typescript", 0.80)

	// Spot-check: the receiver-typed count must be substantial (method calls
	// on fields/params/this/locals all resolve).
	if hist["receiver-typed"] < 5 {
		t.Errorf("TS receiver-typed=%d, want >=5 (field/param/this/local method calls)", hist["receiver-typed"])
	}
}

// TestPythonFixtureGate is the Python regression gate against testdata/py_fixture.
func TestPythonFixtureGate(t *testing.T) {
	database := indexFixture(t, "testdata/py_fixture")
	hist := langResolutionHistogram(t, database, "python")
	// Python typing is optional; this fixture is fully typed, so still expect a
	// strong share, but the gate generalizes to looser real-world code.
	assertModeledLangGate(t, hist, "python", 0.70)

	if hist["receiver-typed"] < 5 {
		t.Errorf("Python receiver-typed=%d, want >=5 (field/param/self method calls)", hist["receiver-typed"])
	}
}
