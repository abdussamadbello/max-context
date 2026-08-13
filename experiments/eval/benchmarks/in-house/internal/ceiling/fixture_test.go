package ceiling

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxcontext/eval/internal/spec"
)

const (
	fixtureDir     = "../../fixtures/payments"
	ceilingProto   = "../../protocol/ceiling-v1.json"
	agentProtoPath = "../../protocol/alias-v4.json"
)

// The fixture originally lived in /tmp and vanished with the machine, which
// made the published 0/5-vs-5/5 result unreproducible. It is committed now, and
// its exact shape IS the answer key's oracle: the key asserts specific files,
// line numbers, and alias names. If the fixture drifts from that text the key
// silently starts grading a different repo, so pin it here.
func TestFixtureMatchesTheOracle(t *testing.T) {
	type site struct {
		file      string
		line      int
		contains  string
		enclosing string
	}
	// Transcribed from alias-v4.json's A01 oracle field.
	sites := []site{
		{"ledger.py", 4, "def post_transaction(", ""},
		{"billing.py", 1, "from ledger import post_transaction as apply_entry", ""},
		{"billing.py", 6, "apply_entry(", "charge_subscription"},
		{"billing.py", 10, "apply_entry(", "issue_refund"},
		{"billing.py", 14, "apply_entry(", "apply_late_fee"},
		{"payroll.py", 1, "from ledger import post_transaction as record_payment", ""},
		{"payroll.py", 7, "record_payment(", "pay_salary"},
		{"payroll.py", 11, "record_payment(", "pay_bonus"},
	}

	for _, s := range sites {
		lines := readLines(filepath.Join(fixtureDir, s.file))
		if len(lines) < s.line {
			t.Errorf("%s has %d lines, oracle references line %d", s.file, len(lines), s.line)
			continue
		}
		got := lines[s.line-1]
		if !strings.Contains(got, s.contains) {
			t.Errorf("%s:%d = %q, oracle expects it to contain %q", s.file, s.line, got, s.contains)
		}
		if fn := enclosingFunc(lines, s.line); fn != s.enclosing {
			t.Errorf("%s:%d is inside %q, oracle expects %q", s.file, s.line, fn, s.enclosing)
		}
	}
}

// The oracle's central claim, restated as an executable check: a text search for
// the real name reaches the definition and the imports, and not one call site.
// If this ever fails the benchmark has stopped testing aliasing.
func TestFixtureCallSitesDoNotNameTheSymbol(t *testing.T) {
	for _, f := range []string{"billing.py", "payroll.py"} {
		lines := readLines(filepath.Join(fixtureDir, f))
		for i, l := range lines {
			if i == 0 {
				continue // the import line names it; that is the point
			}
			if strings.Contains(l, "post_transaction") {
				t.Errorf("%s:%d names post_transaction directly (%q); grep would find this call site "+
					"and the alias test would be vacuous", f, i+1, strings.TrimSpace(l))
			}
		}
	}
}

// The fixture IS the probe repo: whatever sits in it gets indexed and grepped.
// A README dropped in here would be searched for `post_transaction` and would
// quietly change both arms' byte counts, so the directory stays code-only.
// Prose about the fixture lives one level up, in fixtures/README.md.
func TestFixtureContainsOnlySourceFiles(t *testing.T) {
	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"ledger.py": true, "billing.py": true, "payroll.py": true}
	for _, e := range entries {
		if !want[e.Name()] {
			t.Errorf("%s is in the probe repo; it will be indexed and searched along with the code", e.Name())
		}
		delete(want, e.Name())
	}
	for missing := range want {
		t.Errorf("%s is missing from the fixture", missing)
	}
}

// Both gold sets live in two files. The one that is pre-registered and hashed is
// alias-v4.json; the ceiling protocol copies from it. Copies drift.
func TestCeilingProbesMatchTheAnswerKeys(t *testing.T) {
	cp, err := Load(ceilingProto)
	if err != nil {
		t.Fatal(err)
	}
	p, err := spec.Load(agentProtoPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyAgainstKeys(cp, p); err != nil {
		t.Error(err)
	}
}

// A protocol that points at a fixture which is not there would produce a run
// error at the far end of a CI job, or worse, a zero score read as a finding.
func TestLocalFixturesExist(t *testing.T) {
	cp, err := Load(ceilingProto)
	if err != nil {
		t.Fatal(err)
	}
	base := "../.."
	for _, probe := range cp.Probes {
		if probe.RepoPath == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(base, probe.RepoPath)); err != nil {
			t.Errorf("probe %s points at %s, which does not exist: %v", probe.TaskID, probe.RepoPath, err)
		}
	}
}

// Drift guard for the reverse direction: alias-v4.json's payments repo used to
// point at file:///tmp/alias-bench/payments, a path that existed only on the
// machine that authored it.
func TestAgentProtocolPointsAtTheCommittedFixture(t *testing.T) {
	p, err := spec.Load(agentProtoPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range p.Repos {
		if r.Name != "payments" {
			continue
		}
		if strings.Contains(r.CloneURL, "/tmp/") {
			t.Errorf("payments clone_url is %q — a temp path that does not survive the machine that made it", r.CloneURL)
		}
		if !strings.Contains(r.CloneURL, "fixtures/payments") {
			t.Errorf("payments clone_url is %q, want the committed fixture", r.CloneURL)
		}
		return
	}
	t.Fatal("no payments repo in the agent protocol")
}

// Repointing payments at the committed fixture edited a pre-registered file.
// That is only acceptable because clone_url sits outside the hashed subset —
// the tasks, keys and models that grading depends on are untouched. Prove it
// instead of asserting it, so a later change that DOES perturb the
// pre-registration cannot hide behind this one.
func TestFixturePathDoesNotPerturbThePreRegistration(t *testing.T) {
	p, err := spec.Load(agentProtoPath)
	if err != nil {
		t.Fatal(err)
	}
	before := p.Hash()
	for i := range p.Repos {
		p.Repos[i].CloneURL = "file:///tmp/somewhere-else"
		p.Repos[i].SHA = "0000000000000000000000000000000000000000"
	}
	if after := p.Hash(); after != before {
		t.Errorf("changing repo locations changed the protocol hash (%s -> %s); "+
			"the fixture move would have invalidated the pre-registration", before, after)
	}
}

func TestEnclosingFuncAttribution(t *testing.T) {
	src := []string{
		"import os",                // 1
		"",                         // 2
		"def outer(a):",            // 3
		"    x = helper(a)",        // 4
		"    def inner(b):",        // 5
		"        return helper(b)", // 6
		"    return inner(x)",      // 7
		"",                         // 8
		"TOP = helper(1)",          // 9
		"",                         // 10
		"async def fetch():",       // 11
		"    return helper(2)",     // 12
	}
	for _, tc := range []struct {
		line int
		want string
		why  string
	}{
		{4, "outer", "direct body of a function"},
		{6, "inner", "nested def must win over its parent"},
		{7, "outer", "back out of the nested def by indentation"},
		{9, "", "module level has no calling function"},
		{3, "", "a def line belongs to its parent scope, not itself"},
		{12, "fetch", "async def counts"},
	} {
		if got := enclosingFunc(src, tc.line); got != tc.want {
			t.Errorf("line %d: got %q, want %q (%s)", tc.line, got, tc.want, tc.why)
		}
	}
}

func TestEnclosingFuncOutOfRange(t *testing.T) {
	src := []string{"def f():", "    pass"}
	for _, line := range []int{0, -1, 3, 999} {
		if got := enclosingFunc(src, line); got != "" {
			t.Errorf("line %d returned %q, want empty", line, got)
		}
	}
}

func TestDiscoverAliases(t *testing.T) {
	out := strings.Join([]string{
		"./billing.py:1:from ledger import post_transaction as apply_entry",
		"./payroll.py:1:from ledger import post_transaction as record_payment",
		"./ledger.py:4:def post_transaction(account, amount, memo=\"\"):",
		"./other.py:2:from x import something_else as unrelated",
	}, "\n")
	got := discoverAliases(out, "post_transaction")
	want := map[string]bool{"apply_entry": true, "record_payment": true}
	if len(got) != len(want) {
		t.Fatalf("found %v, want exactly %v", got, keysOf(want))
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("found unrelated alias %q", g)
		}
	}
}

// An import that does not rename is not an alias; treating it as one would send
// the chained arm chasing the symbol it already searched for.
func TestDiscoverAliasesIgnoresNonRenames(t *testing.T) {
	out := "./a.py:1:from ledger import post_transaction\n" +
		"./b.py:1:from ledger import post_transaction as post_transaction"
	if got := discoverAliases(out, "post_transaction"); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestScoreRecallAndPrecision(t *testing.T) {
	gold := []string{"a", "b", "c", "d"}
	found, missed, recall, precision := score([]string{"a", "b", "zzz"}, gold)
	if len(found) != 2 || len(missed) != 2 {
		t.Fatalf("found=%v missed=%v", found, missed)
	}
	if recall != 0.5 {
		t.Errorf("recall = %v, want 0.5", recall)
	}
	if fmt.Sprintf("%.4f", precision) != "0.6667" {
		t.Errorf("precision = %v, want 2/3", precision)
	}
}

func keysOf(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
