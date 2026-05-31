// Command report turns results.json into a human-readable report.md with paired
// statistics, effect sizes, per-task deltas, a failure-rate table, and the
// planted grep-win results. Pure post-processing — no API, fully reproducible.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maxcontext/eval-locobench/internal/runlog"
	"github.com/maxcontext/eval-locobench/internal/spec"
	"github.com/maxcontext/eval-locobench/internal/stats"
)

func main() {
	resultsPath := flag.String("results", "results/results.json", "path to results.json")
	protocolPath := flag.String("protocol", "protocol/httpx.json", "path to protocol JSON")
	outPath := flag.String("out", "results/report.md", "path to write report.md")
	flag.Parse()

	if err := gen(*resultsPath, *protocolPath, *outPath); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println("wrote", *outPath)
}

func gen(resultsPath, protocolPath, outPath string) error {
	b, err := os.ReadFile(resultsPath)
	if err != nil {
		return err
	}
	var r runlog.Results
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	p, err := spec.Load(protocolPath)
	if err != nil {
		return err
	}
	taskType := map[string]spec.TaskType{}
	planted := map[string]bool{}
	for _, t := range p.Tasks {
		taskType[t.ID] = t.Type
		planted[t.ID] = t.PlantedGrepWin
	}

	// Index runs by (task, arm); average replicates into one score per arm.
	type pair struct {
		grepScore, mcScore     float64 // normalized 0..1 quality
		grepTok, mcTok         int
		grepTurns, mcTurns     int
		grepCalls, mcCalls     int   // tool-call count
		grepMS, mcMS           int64 // wall-clock ms
		grepStatus, mcStatus   string
		grepOK, mcOK           bool // status == completed (infra failures excluded from quality stats)
		ttype                  spec.TaskType
		planted                bool
		haveGrep, haveMC       bool
	}
	pairs := map[string]*pair{}
	failByArm := map[runlog.Arm]map[string]int{
		runlog.ArmGrep: {}, runlog.ArmMaxContext: {},
	}

	for _, run := range r.Runs {
		pr := pairs[run.TaskID]
		if pr == nil {
			pr = &pair{ttype: taskType[run.TaskID], planted: planted[run.TaskID]}
			pairs[run.TaskID] = pr
		}
		q := quality(run)
		failByArm[run.Arm][string(run.Status)]++
		ok := run.Status == "completed"
		switch run.Arm {
		case runlog.ArmGrep:
			pr.grepScore, pr.grepTok, pr.grepTurns, pr.grepStatus, pr.haveGrep, pr.grepOK = q, run.InputTokens+run.OutputTokens, run.Turns, string(run.Status), true, ok
			pr.grepCalls, pr.grepMS = run.ToolCalls, run.WallClockMS
		case runlog.ArmMaxContext:
			pr.mcScore, pr.mcTok, pr.mcTurns, pr.mcStatus, pr.haveMC, pr.mcOK = q, run.InputTokens+run.OutputTokens, run.Turns, string(run.Status), true, ok
			pr.mcCalls, pr.mcMS = run.ToolCalls, run.WallClockMS
		}
	}

	// Collect paired deltas (mc - grep) for quality and tokens.
	var ids []string
	for id := range pairs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var qualDeltas, tokDeltas, msDeltas, callDeltas []float64
	var mcWin, grepWin int // McNemar discordant counts on "fully correct"
	var rows, effRows []string
	excluded := 0
	for _, id := range ids {
		pr := pairs[id]
		if !pr.haveGrep || !pr.haveMC {
			continue
		}
		// Fairness: only compare tasks where BOTH arms completed. A rate-limit or
		// stdio failure is an infra artifact, not a capability signal — scoring it
		// as a 0-quality loss would bias the result. Excluded tasks remain visible
		// in the failure table below.
		if !pr.grepOK || !pr.mcOK {
			excluded++
			rows = append(rows, fmt.Sprintf("| %s | %s | %s | %s | _excluded (infra)_ | %d | %d |",
				id, pr.ttype, statusMark(pr.grepOK, pr.grepStatus), statusMark(pr.mcOK, pr.mcStatus), pr.grepTok, pr.mcTok))
			continue
		}
		dq := pr.mcScore - pr.grepScore
		qualDeltas = append(qualDeltas, dq)
		tokDeltas = append(tokDeltas, float64(pr.mcTok-pr.grepTok))
		msDeltas = append(msDeltas, float64(pr.mcMS-pr.grepMS))
		callDeltas = append(callDeltas, float64(pr.mcCalls-pr.grepCalls))
		// McNemar on "fully correct" (score==1).
		gc, mc := pr.grepScore >= 0.999, pr.mcScore >= 0.999
		if mc && !gc {
			mcWin++
		} else if gc && !mc {
			grepWin++
		}
		flag := ""
		if pr.planted {
			flag = " 🌱grep-win"
		}
		rows = append(rows, fmt.Sprintf("| %s | %s%s | %.2f | %.2f | %+.2f | %d | %d |",
			id, pr.ttype, flag, pr.grepScore, pr.mcScore, dq, pr.grepTok, pr.mcTok))
		// Efficiency row: time, tool-calls, tokens side by side.
		effRows = append(effRows, fmt.Sprintf("| %s | %d / %d | %d / %d | %.1f / %.1f |",
			id, pr.grepCalls, pr.mcCalls, pr.grepTok, pr.mcTok,
			float64(pr.grepMS)/1000, float64(pr.mcMS)/1000))
	}

	w, n := stats.WilcoxonSignedRank(qualDeltas)
	z, pval := stats.WilcoxonZ(w, n)
	medQ := stats.MedianDelta(qualDeltas)
	loQ, hiQ := stats.BootstrapMedianCI(qualDeltas, 10000, 1, 0.05)
	cliffs := stats.CliffsDelta(qualDeltas)
	chi2, mcP := stats.McNemar(grepWin, mcWin)
	medTok := stats.MedianDelta(tokDeltas)
	medMS := stats.MedianDelta(msDeltas)
	medCalls := stats.MedianDelta(callDeltas)

	var sb strings.Builder
	fmt.Fprintf(&sb, "# Causal A/B Experiment — max-context vs grep\n\n")
	fmt.Fprintf(&sb, "**Protocol hash (pre-registration):** `%s`\n\n", r.Manifest.ProtocolHash)
	fmt.Fprintf(&sb, "**Task model:** %s  **Judge model:** %s (different — no self-judging)\n\n", r.Manifest.TaskModel, r.Manifest.JudgeModel)
	fmt.Fprintf(&sb, "**Repos (pinned):** ")
	for repo, sha := range r.Manifest.RepoSHAs {
		fmt.Fprintf(&sb, "%s@%s ", repo, short(sha))
	}
	fmt.Fprintf(&sb, "\n**rg:** %s  **started:** %s  **replicates:** %d  **max turns:** %d\n\n",
		r.Manifest.RGVersion, r.Manifest.StartedAt, r.Manifest.Replicates, r.Manifest.MaxTurns)

	fmt.Fprintf(&sb, "## Headline (paired, n=%d tasks; %d excluded as infra failures)\n\n", len(qualDeltas), excluded)
	fmt.Fprintf(&sb, "- **Quality delta (mc − grep):** median %+.2f, 95%% bootstrap CI [%+.2f, %+.2f]\n", medQ, loQ, hiQ)
	fmt.Fprintf(&sb, "- **Cliff's delta (direction):** %+.2f (>0 favors max-context)\n", cliffs)
	fmt.Fprintf(&sb, "- **Wilcoxon signed-rank:** W+=%.0f, n=%d, z=%.2f, two-sided p≈%.3f\n", w, n, z, pval)
	fmt.Fprintf(&sb, "- **McNemar (fully-correct discordant):** mc-only wins=%d, grep-only wins=%d, χ²=%.2f, p≈%.3f\n", mcWin, grepWin, chi2, mcP)
	fmt.Fprintf(&sb, "- **Token delta (mc − grep):** median %+.0f tokens/task (negative = max-context cheaper)\n", medTok)
	fmt.Fprintf(&sb, "- **Wall-clock delta (mc − grep):** median %+.1fs/task (negative = max-context faster)\n", medMS/1000)
	fmt.Fprintf(&sb, "- **Tool-call delta (mc − grep):** median %+.1f calls/task (negative = max-context fewer)\n\n", medCalls)
	fmt.Fprintf(&sb, "> Honest scope: n is small. This is an existence+direction+effect-size result on these tasks/repos at pinned SHAs — NOT a universal law. Read per-task deltas and the failure table below.\n\n")

	fmt.Fprintf(&sb, "## Per-task results\n\n")
	fmt.Fprintf(&sb, "| Task | Type | grep | mc | Δ(mc−grep) | grep tok | mc tok |\n|---|---|---|---|---|---|---|\n")
	for _, row := range rows {
		sb.WriteString(row + "\n")
	}

	fmt.Fprintf(&sb, "\n## Efficiency (time & cost) — grep / mc per task\n\n")
	fmt.Fprintf(&sb, "Quality being ~tied, this is where any practical edge shows. Values are grep / max-context.\n\n")
	fmt.Fprintf(&sb, "| Task | tool calls | tokens | wall-clock (s) |\n|---|---|---|---|\n")
	for _, row := range effRows {
		sb.WriteString(row + "\n")
	}

	fmt.Fprintf(&sb, "\n## Failure / status breakdown (no silent drops)\n\n")
	fmt.Fprintf(&sb, "| Status | grep | max-context |\n|---|---|---|\n")
	allStatuses := unionKeys(failByArm[runlog.ArmGrep], failByArm[runlog.ArmMaxContext])
	for _, st := range allStatuses {
		fmt.Fprintf(&sb, "| %s | %d | %d |\n", st, failByArm[runlog.ArmGrep][st], failByArm[runlog.ArmMaxContext][st])
	}

	fmt.Fprintf(&sb, "\n## Planted grep-win tasks\n\n")
	fmt.Fprintf(&sb, "Tasks deliberately chosen to favor grep (prose/\"why\"/dynamic). Reporting these maps the boundary of where max-context helps.\n\n")
	any := false
	for _, id := range ids {
		if pairs[id].planted {
			any = true
			fmt.Fprintf(&sb, "- **%s**: grep %.2f vs mc %.2f (Δ %+.2f)\n", id, pairs[id].grepScore, pairs[id].mcScore, pairs[id].mcScore-pairs[id].grepScore)
		}
	}
	if !any {
		fmt.Fprintf(&sb, "_(none in this protocol)_\n")
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(outPath, []byte(sb.String()), 0o644)
}

// quality maps a run to a normalized 0..1 quality score for pairing.
//
// Retrieval: a "where is X?" answer is correct when the file it ENDORSES (its
// lead file) is a gold file — even if it also names other files to dismiss them.
// So a correct lead scores 1.0; otherwise we fall back to set-F1. This removes
// the precision artifact where dismissed extra files deflated a correct answer
// (FINDINGS: overloaded-name gap was largely a grading bug, not a tool failure).
func quality(run runlog.RunRecord) float64 {
	if run.Retrieval != nil {
		if run.Retrieval.LeadCorrect {
			return 1.0
		}
		return run.Retrieval.F1
	}
	if run.Rubric != nil && run.Rubric.MaxTotal > 0 {
		return float64(run.Rubric.Total) / float64(run.Rubric.MaxTotal)
	}
	return 0 // failed/ungraded runs score 0 but are visible in the failure table
}

// statusMark renders an arm's quality cell, showing the failure status when the
// run did not complete (so excluded tasks are transparent in the table).
func statusMark(ok bool, status string) string {
	if ok {
		return "ok"
	}
	return status
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func unionKeys(a, b map[string]int) []string {
	set := map[string]bool{}
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	var out []string
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
