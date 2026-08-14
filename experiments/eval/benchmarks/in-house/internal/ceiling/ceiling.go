// Package ceiling runs the retrieval-ceiling A/B: it measures what each arm's
// TOOLS put in front of a model, with no model in the loop and no API key.
//
// This is deliberately a weaker claim than the agent experiment in cmd/eval.
// It does not measure whether an agent answers correctly — it measures the
// upper bound on whether it could: if the gold callers never appear in any tool
// output, no model can name them. That bound is objectively checkable, costs
// nothing, and runs on every push.
//
// Both arms are given a generous, declared budget rather than a single call:
//   - grep runs a battery of patterns, and in the chained tier it also follows
//     any import aliases it discovers, which is what a skilled engineer does
//     after reading the first result.
//   - max-context runs its declared tool calls.
//
// Every command, call, and raw output is recorded in the result so the run can
// be audited rather than trusted, and -grep-pattern lets a skeptic add patterns
// and re-run.
package ceiling

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Probe is one objectively-checkable retrieval question, keyed to a task in the
// pre-registered agent protocol so the two experiments describe the same case.
type Probe struct {
	TaskID   string `json:"task_id"`
	Repo     string `json:"repo"`
	Question string `json:"question"`
	Lang     string `json:"lang"`

	// RepoPath is a module-root-relative path to a committed fixture. Local
	// fixtures need no network, which is what lets this run in CI.
	RepoPath string `json:"repo_path,omitempty"`
	// CloneURL/SHA describe a real upstream repo. Probes carrying these are
	// skipped unless the caller opts in, and the skip is reported, never silent.
	CloneURL string `json:"clone_url,omitempty"`
	SHA      string `json:"sha,omitempty"`

	// Symbol is the function whose callers are being sought.
	Symbol string `json:"symbol"`
	// ExpectedSymbols is the gold caller set, copied from the hand-verified key
	// in the agent protocol. VerifyAgainstKeys enforces that it stays in sync.
	ExpectedSymbols []string `json:"expected_symbols"`

	// GrepPatterns is the declared battery for the baseline arm. Patterns are
	// ripgrep regexes; all are run and their results unioned.
	GrepPatterns []string `json:"grep_patterns"`
	// MCCalls is the declared tool sequence for the max-context arm.
	MCCalls []MCCall `json:"mc_calls"`
}

// MCCall is one max-context tool invocation.
type MCCall struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

// CeilingProtocol is the declared probe set. It is a separate file from
// alias-v4.json on purpose: the agent protocol is pre-registered and hashed, and
// adding a no-LLM harness must not perturb it.
type CeilingProtocol struct {
	Version string  `json:"version"`
	Note    string  `json:"note"`
	Probes  []Probe `json:"probes"`
}

// Load reads a ceiling protocol file.
func Load(path string) (*CeilingProtocol, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p CeilingProtocol
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("parse ceiling protocol %s: %w", path, err)
	}
	if len(p.Probes) == 0 {
		return nil, fmt.Errorf("%s declares no probes", path)
	}
	for i, pr := range p.Probes {
		if pr.Symbol == "" || len(pr.ExpectedSymbols) == 0 {
			return nil, fmt.Errorf("probe %d (%s): symbol and expected_symbols are both required", i, pr.TaskID)
		}
		if pr.RepoPath == "" && pr.CloneURL == "" {
			return nil, fmt.Errorf("probe %s: needs repo_path or clone_url", pr.TaskID)
		}
	}
	return &p, nil
}

// NeedsNetwork reports whether this probe must clone a remote repo.
func (p Probe) NeedsNetwork() bool { return p.RepoPath == "" && p.CloneURL != "" }

// Step is one recorded command or tool call and what it returned. Kept in the
// result so a reader can check the arms were run fairly instead of taking the
// score on faith.
type Step struct {
	Arm    string `json:"arm"`
	Detail string `json:"detail"` // the pattern or tool+args actually run
	Bytes  int    `json:"bytes"`  // size of the raw output
	Note   string `json:"note,omitempty"`
}

// ArmResult is one arm's outcome on one probe.
type ArmResult struct {
	Arm string `json:"arm"`
	// ToolCalls and OutputBytes are the cost side. Recall alone would reward an
	// arm for dumping the whole repo, which is exactly the trade being measured.
	ToolCalls   int      `json:"tool_calls"`
	OutputBytes int      `json:"output_bytes"`
	Predicted   []string `json:"predicted"`
	Found       []string `json:"found"`
	Missed      []string `json:"missed"`
	Recall      float64  `json:"recall"`
	Precision   float64  `json:"precision"`
	Steps       []Step   `json:"steps"`
	Err         string   `json:"error,omitempty"`
}

// ProbeResult holds every arm's outcome for one probe.
type ProbeResult struct {
	TaskID   string      `json:"task_id"`
	Repo     string      `json:"repo"`
	Question string      `json:"question"`
	Symbol   string      `json:"symbol"`
	Gold     []string    `json:"gold"`
	Arms     []ArmResult `json:"arms"`
	Skipped  string      `json:"skipped,omitempty"`
}

// Results is the full run.
type Results struct {
	Version string        `json:"version"`
	Probes  []ProbeResult `json:"probes"`
	Skipped []string      `json:"skipped_probes,omitempty"`
}

// score computes recall/precision of a predicted caller set against the gold
// set. Matching is exact on symbol name; the gold sets are function names from
// a hand-verified key, so there is nothing to fuzz.
func score(predicted, gold []string) (found, missed []string, recall, precision float64) {
	pred := map[string]bool{}
	for _, s := range predicted {
		pred[s] = true
	}
	goldSet := map[string]bool{}
	for _, g := range gold {
		goldSet[g] = true
		if pred[g] {
			found = append(found, g)
		} else {
			missed = append(missed, g)
		}
	}
	sort.Strings(found)
	sort.Strings(missed)
	if len(gold) > 0 {
		recall = float64(len(found)) / float64(len(gold))
	}
	if len(pred) > 0 {
		hits := 0
		for s := range pred {
			if goldSet[s] {
				hits++
			}
		}
		precision = float64(hits) / float64(len(pred))
	}
	return found, missed, recall, precision
}

// finalize scores an arm in place. An empty result is [] and never null: a nil
// slice marshals to null, and the arm that finds nothing is precisely the one a
// consumer is checking, so that is where a null would land.
func (a *ArmResult) finalize(gold []string) {
	a.Predicted = nonNil(dedupeSorted(a.Predicted))
	a.Found, a.Missed, a.Recall, a.Precision = score(a.Predicted, gold)
	a.Found, a.Missed = nonNil(a.Found), nonNil(a.Missed)
	if a.Steps == nil {
		a.Steps = []Step{}
	}
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func dedupeSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
