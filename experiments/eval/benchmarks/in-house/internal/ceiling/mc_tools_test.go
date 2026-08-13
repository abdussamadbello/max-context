package ceiling

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// mcBinary returns the max-context binary from MAX_CONTEXT_BIN, or skips.
// CI builds it and sets the variable; locally, `go run ./cmd/ceiling` builds it
// for you.
func mcBinary(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("MAX_CONTEXT_BIN")
	if bin == "" {
		t.Skip("set MAX_CONTEXT_BIN to the max-context binary to run the max-context arm")
	}
	if _, err := exec.LookPath(bin); err != nil {
		if _, err2 := os.Stat(bin); err2 != nil {
			t.Skipf("MAX_CONTEXT_BIN %q not found", bin)
		}
	}
	return bin
}

// stageFixture copies the committed fixture somewhere writable. Indexing creates
// .max-context/, which must never land in the committed tree.
func stageFixture(t *testing.T, probe Probe) string {
	t.Helper()
	dir := t.TempDir()
	if err := CopyDir(filepath.Join("../..", probe.RepoPath), dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The headline result, executed rather than published: on the alias fixture,
// max-context surfaces all five callers in a single tool call.
func TestMaxContextResolvesEveryAliasedCaller(t *testing.T) {
	bin := mcBinary(t)
	probe := aliasProbe(t)
	work := stageFixture(t, probe)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	res := RunMaxContext(ctx, work, probe, bin)
	if res.Err != "" {
		t.Fatalf("max-context arm failed: %s", res.Err)
	}
	if len(res.Found) != len(probe.ExpectedSymbols) {
		t.Errorf("found %v, missed %v; want all %d callers",
			res.Found, res.Missed, len(probe.ExpectedSymbols))
	}
	if res.ToolCalls != 1 {
		t.Errorf("took %d tool calls; the claim is one", res.ToolCalls)
	}
}

// Recall alone would reward an arm for dumping the repo. The claim is that
// max-context reaches the same answer as alias-chained grep for fewer round
// trips — so check the cost, not just the score.
func TestMaxContextReachesTheAnswerInFewerCallsThanGrep(t *testing.T) {
	bin := mcBinary(t)
	rg := needRG(t)
	probe := aliasProbe(t)
	work := stageFixture(t, probe)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	mc := RunMaxContext(ctx, work, probe, bin)
	if mc.Err != "" {
		t.Fatalf("max-context arm failed: %s", mc.Err)
	}
	chained := RunGrep(ctx, work, probe, nil, rg, true)

	if len(mc.Found) != len(chained.Found) {
		t.Fatalf("arms disagree on recall: max-context %d, chained grep %d — "+
			"the cost comparison below is only meaningful at equal recall",
			len(mc.Found), len(chained.Found))
	}
	if mc.ToolCalls >= chained.ToolCalls {
		t.Errorf("max-context took %d calls vs chained grep's %d; the round-trip claim does not hold",
			mc.ToolCalls, chained.ToolCalls)
	}
	t.Logf("equal recall %d/%d — max-context: %d call, %d bytes; chained grep: %d calls, %d bytes",
		len(mc.Found), len(probe.ExpectedSymbols), mc.ToolCalls, mc.OutputBytes,
		chained.ToolCalls, chained.OutputBytes)
}

// An unindexed repo returns empty answers that score as a total loss. That must
// read as a broken setup, not as a finding about max-context.
func TestUnindexedRepoIsAnError(t *testing.T) {
	probe := aliasProbe(t)
	work := stageFixture(t, probe)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res := RunMaxContext(ctx, work, probe, filepath.Join(t.TempDir(), "no-such-binary"))
	if res.Err == "" {
		t.Fatal("a missing binary scored 0/5 silently; that reads as a result")
	}
	if len(res.Found) != 0 {
		t.Errorf("found %v with no working binary", res.Found)
	}
}
