package ceiling

import (
	"fmt"
	"sort"
	"strings"

	"github.com/maxcontext/eval/internal/spec"
)

// VerifyAgainstKeys checks that every probe's gold set still matches the
// hand-verified key for the same task in the agent protocol.
//
// The two files are separate so the pre-registered protocol stays unperturbed,
// and that separation is exactly how a gold set silently drifts. This makes the
// drift a test failure instead.
func VerifyAgainstKeys(cp *CeilingProtocol, p *spec.Protocol) error {
	var problems []string
	for _, probe := range cp.Probes {
		key, ok := p.KeyFor(probe.TaskID)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: no key in the agent protocol", probe.TaskID))
			continue
		}
		if !key.HumanVerified {
			problems = append(problems, fmt.Sprintf("%s: key is not human-verified", probe.TaskID))
		}
		if !sameSet(probe.ExpectedSymbols, key.ExpectedSymbols) {
			problems = append(problems, fmt.Sprintf("%s: gold set drifted\n    ceiling: %v\n    key:     %v",
				probe.TaskID, sorted(probe.ExpectedSymbols), sorted(key.ExpectedSymbols)))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("ceiling probes are out of sync with the answer keys:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := sorted(a), sorted(b)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// Render writes the human-readable report.
func Render(r *Results) string {
	var b strings.Builder
	b.WriteString("# Retrieval ceiling — " + r.Version + "\n\n")
	b.WriteString("Measures what each arm's TOOLS surface, with no model in the loop.\n")
	b.WriteString("Recall is the share of hand-verified gold callers that appear in tool output;\n")
	b.WriteString("an arm that cannot surface a caller sets a ceiling no model can beat.\n\n")

	for _, p := range r.Probes {
		b.WriteString(fmt.Sprintf("## %s — %s\n\n", p.TaskID, p.Repo))
		if p.Skipped != "" {
			b.WriteString("SKIPPED: " + p.Skipped + "\n\n")
			continue
		}
		b.WriteString(p.Question + "\n\n")
		b.WriteString(fmt.Sprintf("Gold callers (%d): %s\n\n", len(p.Gold), strings.Join(sorted(p.Gold), ", ")))
		b.WriteString("| Arm | Recall | Precision | Tool calls | Output bytes | Missed |\n")
		b.WriteString("|---|---|---|---|---|---|\n")
		for _, a := range p.Arms {
			missed := "—"
			if len(a.Missed) > 0 {
				missed = strings.Join(a.Missed, ", ")
			}
			note := ""
			if a.Err != "" {
				note = " (error: " + a.Err + ")"
			}
			b.WriteString(fmt.Sprintf("| %s%s | %d/%d | %.2f | %d | %d | %s |\n",
				a.Arm, note, len(a.Found), len(p.Gold), a.Precision, a.ToolCalls, a.OutputBytes, missed))
		}
		b.WriteString("\n<details><summary>Every call that was run</summary>\n\n")
		for _, a := range p.Arms {
			for _, s := range a.Steps {
				b.WriteString(fmt.Sprintf("- `%s` → %s (%d bytes)", s.Detail, s.Arm, s.Bytes))
				if s.Note != "" {
					b.WriteString("  \n  " + s.Note)
				}
				b.WriteString("\n")
			}
		}
		b.WriteString("\n</details>\n\n")
	}

	if len(r.Skipped) > 0 {
		b.WriteString("## Skipped\n\n")
		for _, s := range r.Skipped {
			b.WriteString("- " + s + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Summary is the one-line-per-arm terminal output.
func Summary(r *Results) string {
	var b strings.Builder
	for _, p := range r.Probes {
		if p.Skipped != "" {
			b.WriteString(fmt.Sprintf("%-5s %-10s SKIPPED (%s)\n", p.TaskID, p.Repo, p.Skipped))
			continue
		}
		for _, a := range p.Arms {
			status := ""
			if a.Err != "" {
				status = "  ERROR: " + a.Err
			}
			b.WriteString(fmt.Sprintf("%-5s %-10s %-20s recall %d/%d  calls %d  bytes %d%s\n",
				p.TaskID, p.Repo, a.Arm, len(a.Found), len(p.Gold), a.ToolCalls, a.OutputBytes, status))
		}
	}
	return b.String()
}
