package ceiling

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"
)

func needRG(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed; the baseline arm needs it")
	}
	return "rg"
}

func aliasProbe(t *testing.T) Probe {
	t.Helper()
	cp, err := Load(ceilingProto)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range cp.Probes {
		if p.TaskID == "A01" {
			return p
		}
	}
	t.Fatal("probe A01 missing from the ceiling protocol")
	return Probe{}
}

// The claim under test, run against the committed fixture instead of asserted
// in a markdown file: searching for the symbol's real name reaches none of its
// five callers, because every call site reads an alias.
func TestOneShotGrepMissesEveryAliasedCaller(t *testing.T) {
	rg := needRG(t)
	p := aliasProbe(t)

	res := RunGrep(context.Background(), fixtureDir, p, nil, rg, false)
	if res.Err != "" {
		t.Fatalf("grep arm failed: %s", res.Err)
	}
	if len(res.Found) != 0 {
		t.Errorf("one-shot grep found %v; the oracle says a name search reaches 0 of 5 callers", res.Found)
	}
	if len(res.Missed) != len(p.ExpectedSymbols) {
		t.Errorf("missed %d of %d", len(res.Missed), len(p.ExpectedSymbols))
	}
	if res.ToolCalls != len(p.GrepPatterns) {
		t.Errorf("ran %d searches, expected the whole declared battery of %d", res.ToolCalls, len(p.GrepPatterns))
	}
}

// And the other half of an honest comparison: a skilled engineer does not stop
// at the first result. Following the aliases the first search revealed finds
// every caller — so the finding is a cost difference, not an impossibility.
// Publishing only the one-shot number would be a strawman.
func TestAliasChainedGrepFindsThemAll(t *testing.T) {
	rg := needRG(t)
	p := aliasProbe(t)

	res := RunGrep(context.Background(), fixtureDir, p, nil, rg, true)
	if res.Err != "" {
		t.Fatalf("grep arm failed: %s", res.Err)
	}
	if len(res.Found) != len(p.ExpectedSymbols) {
		t.Errorf("chained grep found %v, missed %v; it should reach all %d",
			res.Found, res.Missed, len(p.ExpectedSymbols))
	}
	if res.ToolCalls <= len(p.GrepPatterns) {
		t.Errorf("chained arm ran %d searches, same as one-shot; it did not follow the aliases", res.ToolCalls)
	}
}

// The extra searches are the cost the chained arm pays, and the report is only
// meaningful if that cost is actually recorded.
func TestChainedGrepCostsMoreThanOneShot(t *testing.T) {
	rg := needRG(t)
	p := aliasProbe(t)
	ctx := context.Background()

	one := RunGrep(ctx, fixtureDir, p, nil, rg, false)
	chained := RunGrep(ctx, fixtureDir, p, nil, rg, true)

	if chained.ToolCalls <= one.ToolCalls {
		t.Errorf("chained ran %d calls vs one-shot %d", chained.ToolCalls, one.ToolCalls)
	}
	if chained.OutputBytes <= one.OutputBytes {
		t.Errorf("chained read %d bytes vs one-shot %d", chained.OutputBytes, one.OutputBytes)
	}
}

// A skeptic must be able to add patterns and re-run rather than take the
// baseline's configuration on faith.
func TestExtraPatternsAreHonoured(t *testing.T) {
	rg := needRG(t)
	p := aliasProbe(t)

	res := RunGrep(context.Background(), fixtureDir, p, []string{`apply_entry\s*\(`}, rg, false)
	if res.ToolCalls != len(p.GrepPatterns)+1 {
		t.Fatalf("ran %d searches, want the battery plus the extra", res.ToolCalls)
	}
	// That one pattern reaches billing.py's three callers, and only those.
	for _, want := range []string{"charge_subscription", "issue_refund", "apply_late_fee"} {
		if !contains(res.Found, want) {
			t.Errorf("extra pattern did not reach %s (found %v)", want, res.Found)
		}
	}
}

// Every search that ran is recorded, so the run can be audited instead of
// trusted.
func TestStepsRecordEveryCall(t *testing.T) {
	rg := needRG(t)
	p := aliasProbe(t)

	res := RunGrep(context.Background(), fixtureDir, p, nil, rg, true)
	if len(res.Steps) != res.ToolCalls {
		t.Errorf("%d steps recorded for %d calls", len(res.Steps), res.ToolCalls)
	}
	var annotated int
	for _, s := range res.Steps {
		if s.Note != "" {
			annotated++
		}
	}
	if annotated != 2 {
		t.Errorf("%d follow-up searches annotated, want 2 (one per alias)", annotated)
	}
}

// The arm that finds nothing is the one a consumer checks first, and a nil Go
// slice marshals to JSON null — so `len(arm["found"])` would explode on exactly
// the interesting row. Caught by the CI assertion doing that.
func TestEmptyResultsMarshalAsArraysNotNull(t *testing.T) {
	rg := needRG(t)
	p := aliasProbe(t)

	res := RunGrep(context.Background(), fixtureDir, p, nil, rg, false)
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]interface{}
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"found", "missed", "predicted", "steps"} {
		if back[field] == nil {
			t.Errorf("%q marshalled as null; consumers indexing it will crash", field)
		}
	}
}

func TestParseHitsHandlesColonsInText(t *testing.T) {
	hits := parseHits("./a.py:12:    url = \"http://x:8080/y\"\n./b.py:3:ok\n")
	if len(hits) != 2 {
		t.Fatalf("parsed %d hits, want 2: %+v", len(hits), hits)
	}
	if hits[0].Path != "a.py" || hits[0].Line != 12 {
		t.Errorf("first hit = %+v", hits[0])
	}
	if hits[0].Text != "    url = \"http://x:8080/y\"" {
		t.Errorf("text was truncated at a colon: %q", hits[0].Text)
	}
}

func TestHarvestNamesWalksNestedJSON(t *testing.T) {
	body := `{"function":"post_transaction","callers":[{"name":"pay_salary","depth":1},
		{"name":"pay_bonus","nested":{"name":"deep"}}],"truncated":false}`
	got := harvestNames(body)
	for _, want := range []string{"pay_salary", "pay_bonus", "deep"} {
		if !contains(got, want) {
			t.Errorf("missed %q in %v", want, got)
		}
	}
}

func TestHarvestNamesOnNonJSON(t *testing.T) {
	if got := harvestNames("not json at all"); len(got) != 0 {
		t.Errorf("got %v, want nothing", got)
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
