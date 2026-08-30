package ceiling

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxcontext/eval/internal/spec"
)

const (
	dispatchFixtureDir = "../../fixtures/dispatch"
	dispatchCeiling    = "../../protocol/ceiling-v2-dispatch.json"
	dispatchAgentProto = "../../protocol/dispatch-v5.json"
)

// The key's oracle cites exact files and line numbers. Pin them, as
// fixtures/payments is pinned, so the key cannot start grading a different repo.
func TestDispatchFixtureMatchesTheOracle(t *testing.T) {
	for _, s := range []struct {
		file      string
		line      int
		contains  string
		enclosing string
	}{
		{"notifier.go", 6, "type Notifier interface {", ""},
		{"notifier.go", 7, "Send(msg string) error", ""},
		{"email.go", 8, "func (e *EmailNotifier) Send(", ""},
		{"sms.go", 10, "func (s *SMSNotifier) Send(", ""},
		{"pipeline.go", 8, "n.Send(msg)", "DeliverAlert"},
		{"pipeline.go", 13, "n.Send(msg)", "BroadcastAll"},
		{"metrics.go", 15, "func (m *MetricsBuffer) Send(", ""},
		{"metrics.go", 22, "m.Send(msg)", "FlushMetrics"},
	} {
		lines := readLines(filepath.Join(dispatchFixtureDir, s.file))
		if len(lines) < s.line {
			t.Errorf("%s has %d lines, oracle cites line %d", s.file, len(lines), s.line)
			continue
		}
		if got := lines[s.line-1]; !strings.Contains(got, s.contains) {
			t.Errorf("%s:%d = %q, oracle expects it to contain %q", s.file, s.line, got, s.contains)
		}
		if fn := enclosingFunc(s.file, lines, s.line); fn != s.enclosing {
			t.Errorf("%s:%d is inside %q, oracle expects %q", s.file, s.line, fn, s.enclosing)
		}
	}
}

// The whole probe depends on the interface being declared across multiple lines:
// the single-line form `interface{ Send(...) }` disables method extraction, so
// reformatting this one line would silently turn the probe into a measurement of
// that parser bug instead of of dispatch resolution.
func TestDispatchInterfaceIsDeclaredMultiLine(t *testing.T) {
	lines := readLines(filepath.Join(dispatchFixtureDir, "notifier.go"))
	for _, l := range lines {
		if strings.Contains(l, "interface{") && strings.Contains(l, "Send") {
			t.Fatalf("Notifier is declared single-line (%q); method extraction fails in that form", strings.TrimSpace(l))
		}
	}
}

// isComment reports whether a line is a Go line comment. The properties below
// are about what the CODE says: a comment naming a type is documentation, and
// the indexer does not resolve calls out of it.
func isComment(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "//") }

// The property the probe rests on: no call site names a concrete implementation.
// If one did, plain text search would reach it and the fixture would stop
// isolating dispatch.
func TestDispatchCallSitesNameNoImplementation(t *testing.T) {
	for _, file := range []string{"pipeline.go"} {
		for i, l := range readLines(filepath.Join(dispatchFixtureDir, file)) {
			if isComment(l) {
				continue
			}
			for _, impl := range []string{"EmailNotifier", "SMSNotifier"} {
				if strings.Contains(l, impl) {
					t.Errorf("%s:%d names %s (%q); call sites must reach implementations only through Notifier",
						file, i+1, impl, strings.TrimSpace(l))
				}
			}
		}
	}
}

// The decoy must stay a decoy: MetricsBuffer is only useful if it is never used
// as a Notifier, so that surfacing FlushMetrics is unambiguously a precision loss.
func TestDispatchDecoyIsNeverUsedAsNotifier(t *testing.T) {
	for i, l := range readLines(filepath.Join(dispatchFixtureDir, "metrics.go")) {
		if isComment(l) {
			continue
		}
		if strings.Contains(l, "Notifier") {
			t.Errorf("metrics.go:%d mentions Notifier in code (%q); the decoy must not be one",
				i+1, strings.TrimSpace(l))
		}
	}
}

// Gold sets live in two files. Verify they agree, the same guard ceiling-v1 has.
func TestDispatchProbesMatchTheAnswerKeys(t *testing.T) {
	cp, err := Load(dispatchCeiling)
	if err != nil {
		t.Fatalf("load ceiling protocol: %v", err)
	}
	keys, err := spec.Load(dispatchAgentProto)
	if err != nil {
		t.Fatalf("load answer keys: %v", err)
	}
	if err := VerifyAgainstKeys(cp, keys); err != nil {
		t.Error(err)
	}
}

// D01 and D02 are a paired measurement: same repo, same question, same gold set,
// differing only in min_confidence. If they drift apart the delta between them
// stops meaning "what the flag buys".
func TestDispatchProbePairDiffersOnlyByConfidence(t *testing.T) {
	cp, err := Load(dispatchCeiling)
	if err != nil {
		t.Fatalf("load ceiling protocol: %v", err)
	}
	var d01, d02 *Probe
	for i := range cp.Probes {
		switch cp.Probes[i].TaskID {
		case "D01":
			d01 = &cp.Probes[i]
		case "D02":
			d02 = &cp.Probes[i]
		}
	}
	if d01 == nil || d02 == nil {
		t.Fatal("expected probes D01 and D02")
	}
	if d01.RepoPath != d02.RepoPath || d01.Question != d02.Question || d01.Symbol != d02.Symbol {
		t.Error("D01 and D02 must share repo, question, and symbol")
	}
	if !sameSet(d01.ExpectedSymbols, d02.ExpectedSymbols) {
		t.Error("D01 and D02 must share a gold set")
	}
	if strings.Contains(string(d01.MCCalls[0].Args), "min_confidence") {
		t.Error("D01 is the default-confidence arm; it must not set min_confidence")
	}
	if !strings.Contains(string(d02.MCCalls[0].Args), "interface-dispatch") {
		t.Error("D02 is the opt-in arm; it must set min_confidence to interface-dispatch")
	}
}
