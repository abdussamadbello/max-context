// Command pool aggregates one or more results.json files into a single paired
// analysis across arms. It exists because the headline question — does
// max-context (or hybrid) beat grep on closed-ended tasks — is only answerable
// on scenarios where the COMPARED arms both produced an answer; truncations and
// errors must be excluded from quality, not silently scored 0.
//
// For each pair of arms it reports, over scenarios where BOTH completed:
//   - quality: summed rubric points and % of max
//   - tokens: summed input tokens and the mc/grep-style ratio
//   - a sign test (wins/losses/ties by per-scenario rubric score) so a small-n
//     result isn't over-read as a mean.
//
// Usage: go run ./cmd/pool results-batch2/results.json results-batch3/results.json
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/maxcontext/eval-locobench/internal/runlog"
)

type scored struct {
	status   string
	inTok    int
	points   int
	maxPts   int
	graded   bool
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: pool <results.json> [results.json ...]")
		os.Exit(1)
	}

	// scenario id -> arm -> scored
	data := map[string]map[runlog.Arm]scored{}
	armsSeen := map[runlog.Arm]bool{}
	for _, path := range os.Args[1:] {
		b, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read", path, ":", err)
			os.Exit(1)
		}
		var res runlog.Results
		if err := json.Unmarshal(b, &res); err != nil {
			fmt.Fprintln(os.Stderr, "parse", path, ":", err)
			os.Exit(1)
		}
		for _, run := range res.Runs {
			if _, ok := data[run.TaskID]; !ok {
				data[run.TaskID] = map[runlog.Arm]scored{}
			}
			s := scored{status: string(run.Status), inTok: run.InputTokens}
			if run.Rubric != nil {
				s.points = run.Rubric.Total
				s.maxPts = run.Rubric.MaxTotal
				s.graded = true
			}
			data[run.TaskID][run.Arm] = s
			armsSeen[run.Arm] = true
		}
	}

	arms := []runlog.Arm{}
	for _, a := range []runlog.Arm{runlog.ArmGrep, runlog.ArmMaxContext, runlog.ArmHybrid} {
		if armsSeen[a] {
			arms = append(arms, a)
		}
	}

	fmt.Printf("Pooled over %d scenarios, arms: %v\n\n", len(data), arms)

	// Per-arm completion + raw quality (all scenarios, for transparency).
	fmt.Println("=== completion + raw quality (all scenarios) ===")
	for _, a := range arms {
		comp, trunc, pts, mx := 0, 0, 0, 0
		for _, byArm := range data {
			s, ok := byArm[a]
			if !ok {
				continue
			}
			if s.status == "completed" {
				comp++
			} else {
				trunc++
			}
			if s.graded {
				pts += s.points
				mx += s.maxPts
			}
		}
		fmt.Printf("  %-12s completed=%-3d non-completed=%-3d  quality=%d/%d (%.0f%%)\n",
			a, comp, trunc, pts, mx, pctOf(pts, mx))
	}

	// Paired comparisons: only scenarios where BOTH arms in the pair completed.
	fmt.Println("\n=== paired comparisons (both arms completed) ===")
	for i := 0; i < len(arms); i++ {
		for j := i + 1; j < len(arms); j++ {
			a, b := arms[i], arms[j]
			paired(data, a, b)
		}
	}
}

func paired(data map[string]map[runlog.Arm]scored, a, b runlog.Arm) {
	var n, aPts, bPts, mx, aTok, bTok, aWin, bWin, tie int
	ids := make([]string, 0, len(data))
	for id := range data {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		sa, oka := data[id][a]
		sb, okb := data[id][b]
		if !oka || !okb || sa.status != "completed" || sb.status != "completed" || !sa.graded || !sb.graded {
			continue
		}
		n++
		aPts += sa.points
		bPts += sb.points
		mx += sa.maxPts // same scenario → same max
		aTok += sa.inTok
		bTok += sb.inTok
		switch {
		case sa.points > sb.points:
			aWin++
		case sb.points > sa.points:
			bWin++
		default:
			tie++
		}
	}
	if n == 0 {
		fmt.Printf("  %s vs %s: no jointly-completed scenarios\n", a, b)
		return
	}
	fmt.Printf("  %s vs %s  (n=%d both completed)\n", a, b, n)
	fmt.Printf("      quality: %s %d/%d (%.0f%%)  |  %s %d/%d (%.0f%%)\n",
		a, aPts, mx, pctOf(aPts, mx), b, bPts, mx, pctOf(bPts, mx))
	fmt.Printf("      tokens:  %s %d  |  %s %d  (%s/%s = %.2fx)\n",
		a, aTok, b, bTok, b, a, ratio(bTok, aTok))
	fmt.Printf("      sign test (by rubric score): %s wins %d, %s wins %d, ties %d\n",
		a, aWin, b, bWin, tie)
}

func pctOf(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}
