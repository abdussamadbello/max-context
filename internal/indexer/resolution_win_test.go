package indexer

import (
	"testing"
)

// TestResolutionWin quantifies the precision improvement on the self-repo
// relative to the v1 baseline, where EVERY edge was a name-global guess. It
// reports, for Go edges:
//   - how many are now precisely linked (same-file/same-package/receiver-typed)
//   - how many false positives were removed (import-qualified/builtin — these
//     were previously linked to some same-named symbol, now correctly not)
//   - how many remain at the deterministic ceiling (unresolved)
//
// Informational (always passes); the hard gate lives in TestResolutionBaseline.
func TestResolutionWin(t *testing.T) {
	database := indexSelfRepo(t)

	goHist, err := goResolutionHistogram(database)
	if err != nil {
		t.Fatalf("goResolutionHistogram: %v", err)
	}
	total := 0
	for _, n := range goHist {
		total += n
	}
	if total == 0 {
		t.Fatal("no Go edges")
	}

	precise := goHist["same-file"] + goHist["same-package"] + goHist["receiver-typed"]
	removedFalse := goHist["import-qualified"] + goHist["builtin"]
	ceiling := goHist["unresolved"]

	pct := func(n int) float64 { return 100 * float64(n) / float64(total) }

	t.Logf("Go call-edge precision win (vs v1 baseline of 100%% name-global, n=%d):", total)
	t.Logf("  precisely linked       %4d  (%.1f%%)  [same-file/same-package/receiver-typed]", precise, pct(precise))
	t.Logf("  false positives removed %4d  (%.1f%%)  [import-qualified/builtin — no longer mislinked]", removedFalse, pct(removedFalse))
	t.Logf("  deterministic ceiling  %4d  (%.1f%%)  [unresolved — needs type inference]", ceiling, pct(ceiling))
	t.Logf("  legacy name-global     %4d  (%.1f%%)  [should be 0 for Go]", goHist["name-global"], pct(goHist["name-global"]))
	t.Logf("=> %.1f%% of Go edges moved from a blind guess to a precise link or honest classification", pct(precise+removedFalse))
}
