// Command eval runs the LoCoBench causal A/B: for each scenario, the same model
// answers once with grep tools (Arm A) and once with max-context over stdio MCP
// (Arm B). Both arms receive only the task (discovery mode strips LoCoBench's
// pre-supplied file list); each gathers context its own way. Answers are graded
// by a blind, different-model judge against the scenario's own ground_truth +
// evaluation_criteria. Identical grading per arm → the measured delta is purely
// "how the agent gathers context."
//
// Unlike the in-house eval, repos are NOT git-cloned: each scenario's project is
// materialized from the extracted LoCoBench data on disk.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/maxcontext/eval-locobench/internal/agent"
	"github.com/maxcontext/eval-locobench/internal/arms"
	"github.com/maxcontext/eval-locobench/internal/grade"
	"github.com/maxcontext/eval-locobench/internal/runlog"
	"github.com/maxcontext/eval-locobench/internal/scenario"
	"github.com/maxcontext/eval-locobench/internal/spec"
)

type backendOpts struct {
	kind        string
	awsRegion   string
	awsProfile  string
	modelPrefix string
	taskModel   string
	judgeModel  string
}

func main() {
	var (
		dataDir    = flag.String("data", "data", "extracted LoCoBench data root (holds generated/ and output/)")
		filterLang = flag.String("lang", "", "comma-separated language prefixes to include (e.g. go,typescript); empty = all")
		filterCat  = flag.String("category", "", "comma-separated task categories to include; empty = all")
		filterDiff = flag.String("difficulty", "", "comma-separated difficulties to include (easy,medium,hard,expert); empty = all")
		idList     = flag.String("id-list", "", "path to a newline-separated file of scenario ids to include (e.g. fair-fight-subset.txt); empty = all")
		limit      = flag.Int("limit", 0, "max scenarios to run after filtering (0 = all)")
		intentMode = flag.String("intent", "discovery", "prompt mode: discovery (strip file list) | full (verbatim task_prompt)")
		armsFlag   = flag.String("arms", "grep,max-context", "comma-separated arms to run, paired per scenario: grep,max-context,hybrid")

		outDir      = flag.String("out", "results", "output dir")
		mcBin       = flag.String("mc-bin", "max-context", "path to max-context binary")
		rgPath      = flag.String("rg", "rg", "path to ripgrep binary")
		taskModelF  = flag.String("model", "claude-sonnet-4-6", "task model under test")
		judgeModelF = flag.String("judge", "claude-haiku-4-5-20251001", "judge model (must differ from task model)")
		maxTurns    = flag.Int("max-turns", 15, "per-run turn budget")
		tokenBudget = flag.Int("token-budget", 0, "per-run total token guardrail (0 = unlimited)")

		backend    = flag.String("backend", "anthropic", "anthropic | bedrock")
		awsRegion  = flag.String("aws-region", "us-east-1", "AWS region (bedrock)")
		awsProfile = flag.String("aws-profile", "", "AWS profile (bedrock)")
		prefix     = flag.String("model-prefix", "", "prefix prepended to model ids (e.g. 'us.anthropic.' for Bedrock)")
		taskOv     = flag.String("task-model", "", "override task model id entirely")
		judgeOv    = flag.String("judge-model", "", "override judge model id entirely")

		dryRun = flag.Bool("dry-run", false, "load + materialize + index, skip LLM calls")
	)
	flag.Parse()

	mode := scenario.IntentDiscovery
	if *intentMode == "full" {
		mode = scenario.IntentFull
	}

	armsToRun, err := parseArms(*armsFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	bo := backendOpts{kind: *backend, awsRegion: *awsRegion, awsProfile: *awsProfile,
		modelPrefix: *prefix, taskModel: *taskOv, judgeModel: *judgeOv}

	ids, err := loadIDList(*idList)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	cfg := runConfig{
		dataDir: *dataDir, outDir: *outDir, mcBin: *mcBin, rgPath: *rgPath,
		taskModel: *taskModelF, judgeModel: *judgeModelF, maxTurns: *maxTurns,
		tokenBudget: *tokenBudget, mode: mode, dryRun: *dryRun, arms: armsToRun,
		filter: filter{
			langs: splitCSV(*filterLang), cats: splitCSV(*filterCat),
			diffs: splitCSV(*filterDiff), limit: *limit, ids: ids,
		},
	}
	if err := run(cfg, bo); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type filter struct {
	langs, cats, diffs []string
	ids                map[string]bool // explicit id allowlist (nil = no id filter)
	limit              int
}

// loadIDList reads a newline-separated scenario-id allowlist (e.g. the
// fair-fight subset). Empty path → nil (no id filtering). Blank lines ignored.
func loadIDList(path string) (map[string]bool, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read id list %s: %w", path, err)
	}
	ids := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			ids[t] = true
		}
	}
	return ids, nil
}

type runConfig struct {
	dataDir, outDir, mcBin, rgPath string
	taskModel, judgeModel          string
	maxTurns, tokenBudget          int
	mode                           scenario.IntentMode
	dryRun                         bool
	arms                           []runlog.Arm
	filter                         filter
}

// parseArms parses the --arms CSV into validated arm identifiers, preserving
// order (each scenario runs its arms in this sequence). Rejects unknown names so
// a typo can't silently drop an arm.
func parseArms(s string) ([]runlog.Arm, error) {
	valid := map[string]runlog.Arm{
		string(runlog.ArmGrep):       runlog.ArmGrep,
		string(runlog.ArmMaxContext): runlog.ArmMaxContext,
		string(runlog.ArmHybrid):     runlog.ArmHybrid,
	}
	var out []runlog.Arm
	for _, p := range splitCSV(s) {
		a, ok := valid[p]
		if !ok {
			return nil, fmt.Errorf("unknown arm %q (valid: grep, max-context, hybrid)", p)
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--arms is empty")
	}
	return out, nil
}

func run(cfg runConfig, bo backendOpts) error {
	// Load + filter scenarios.
	scenariosDir := filepath.Join(cfg.dataDir, "output", "scenarios")
	scns, err := scenario.LoadDir(scenariosDir, cfg.filter.keep)
	if err != nil {
		return fmt.Errorf("load scenarios: %w", err)
	}
	if len(scns) == 0 {
		return fmt.Errorf("no scenarios matched filters under %s", scenariosDir)
	}
	if cfg.filter.limit > 0 && len(scns) > cfg.filter.limit {
		scns = scns[:cfg.filter.limit]
	}
	fmt.Printf("loaded %d scenario(s) after filtering\n", len(scns))

	// Build a Protocol so the existing hash/audit machinery applies.
	p := &spec.Protocol{
		Version:    "locobench-2509.09614",
		TaskModel:  cfg.taskModel,
		JudgeModel: cfg.judgeModel,
		Replicates: 1,
		MaxTurns:   cfg.maxTurns,
		TokenBudget: cfg.tokenBudget,
		SystemSkeleton: "You are a senior software engineer working in an unfamiliar codebase. " +
			"Use the available tools to investigate the code, then answer the task precisely and concretely.",
	}
	for _, s := range scns {
		task, key := scenario.ToTask(s, cfg.mode)
		p.Tasks = append(p.Tasks, task)
		p.Keys = append(p.Keys, key)
	}
	hash := p.Hash()
	fmt.Printf("protocol hash (pre-registration): %s\n", hash)

	// Resolve backend model ids (mirrors in-house resolution).
	if bo.taskModel != "" {
		p.TaskModel = bo.taskModel
	} else {
		p.TaskModel = bo.modelPrefix + p.TaskModel
	}
	if bo.judgeModel != "" {
		p.JudgeModel = bo.judgeModel
	} else {
		p.JudgeModel = bo.modelPrefix + p.JudgeModel
	}
	fmt.Printf("task model: %s  judge model: %s\n", p.TaskModel, p.JudgeModel)

	var client agent.Caller
	switch bo.kind {
	case "bedrock":
		client = agent.NewBedrockClient(bo.awsRegion, bo.awsProfile)
	default:
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" && !cfg.dryRun {
			return fmt.Errorf("ANTHROPIC_API_KEY not set (use --dry-run, or --backend bedrock)")
		}
		client = agent.NewClient(apiKey)
	}
	judge := grade.NewJudge(client, p.JudgeModel)

	// Preflight the grep arm so a broken baseline aborts loudly.
	pfCtx, cancelPF := context.WithTimeout(context.Background(), 30*time.Second)
	if err := arms.NewGrepArmWithRG(cfg.dataDir, cfg.rgPath).Preflight(pfCtx); err != nil {
		cancelPF()
		return fmt.Errorf("grep arm preflight failed: %w", err)
	}
	cancelPF()

	if err := os.MkdirAll(filepath.Join(cfg.outDir, "transcripts"), 0o755); err != nil {
		return err
	}

	manifest := runlog.Manifest{
		ProtocolHash: hash, ProtocolFile: "(generated from scenarios: " + scenariosDir + ")",
		TaskModel: p.TaskModel, JudgeModel: p.JudgeModel, Temperature: 0,
		MaxTurns: p.MaxTurns, TokenBudget: p.TokenBudget, Replicates: 1,
		RepoSHAs: map[string]string{}, IndexSHAs: map[string]string{},
		MaxContextBin: cfg.mcBin, RGVersion: rgVersionAt(cfg.rgPath),
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		GrepPlaybook: arms.GrepPlaybook, MCPlaybook: arms.MCPlaybook,
	}

	// Resolve + index each scenario's project once (dedup by prefix: many
	// scenarios share a project).
	innerRoots := map[string]string{}   // scenario id -> inner project root (search/index target)
	indexed := map[string]bool{}        // inner root -> already indexed
	for _, s := range scns {
		_, inner, err := s.RepoRoot(cfg.dataDir)
		if err != nil {
			return fmt.Errorf("resolve project for %s: %w", s.ID, err)
		}
		innerRoots[s.ID] = inner
		if !indexed[inner] {
			if err := indexRepo(cfg.mcBin, inner); err != nil {
				return fmt.Errorf("index %s: %w", inner, err)
			}
			indexed[inner] = true
			manifest.IndexSHAs[filepath.Base(filepath.Dir(inner))] = "local-materialized"
			fmt.Printf("prepared project %s\n", filepath.Base(filepath.Dir(inner)))
		}
	}

	if cfg.dryRun {
		fmt.Printf("dry-run: %d scenarios loaded, %d projects indexed, arms wired. Skipping LLM calls.\n",
			len(scns), len(indexed))
		return writeResults(cfg.outDir, runlog.Results{Manifest: manifest})
	}

	var records []runlog.RunRecord
	for _, s := range scns {
		task := p.Tasks[indexOf(p.Tasks, s.ID)]
		key, _ := p.KeyFor(s.ID)
		root := innerRoots[s.ID]

		// Run each configured arm on the SAME scenario (paired). Order follows
		// cfg.arms so transcripts/checkpoints group predictably.
		briefs := make([]string, 0, len(cfg.arms))
		for _, arm := range cfg.arms {
			rec := runTask(client, judge, p, task, key, arm, root, cfg.mcBin, cfg.rgPath, cfg.outDir)
			records = append(records, rec)
			briefs = append(briefs, fmt.Sprintf("%s=%s/%s", arm, rec.Status, scoreBrief(rec)))
		}
		fmt.Printf("  %s: %s\n", s.ID, strings.Join(briefs, "  "))

		if err := writeResults(cfg.outDir, runlog.Results{Manifest: manifest, Runs: records}); err != nil {
			return err
		}
		time.Sleep(pace)
	}
	return writeResults(cfg.outDir, runlog.Results{Manifest: manifest, Runs: records})
}

const pace = 5 * time.Second

func runTask(client agent.Caller, judge *grade.Judge, p *spec.Protocol, task spec.Task, key spec.Key,
	arm runlog.Arm, root, mcBin, rgPath, outDir string) runlog.RunRecord {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	rec := runlog.RunRecord{TaskID: task.ID, Repo: task.Repo, Arm: arm, Replicate: 0, Phrasing: task.Intent}
	system := p.SystemSkeleton + "\n\n" + playbookFor(arm)

	var exec agent.ToolExecutor
	switch arm {
	case runlog.ArmMaxContext:
		c, err := arms.StartMCP(ctx, mcBin, root)
		if err != nil {
			rec.Status = "mcp_stdio_failure"
			rec.GradeErr = err.Error()
			return rec
		}
		defer c.Close()
		marm, err := arms.NewMCPArm(ctx, c)
		if err != nil {
			rec.Status = "mcp_stdio_failure"
			rec.GradeErr = err.Error()
			return rec
		}
		exec = marm
	case runlog.ArmHybrid:
		// Realistic deployment: grep+read+list AND the MC tools at once.
		c, err := arms.StartMCP(ctx, mcBin, root)
		if err != nil {
			rec.Status = "mcp_stdio_failure"
			rec.GradeErr = err.Error()
			return rec
		}
		defer c.Close()
		marm, err := arms.NewMCPArm(ctx, c)
		if err != nil {
			rec.Status = "mcp_stdio_failure"
			rec.GradeErr = err.Error()
			return rec
		}
		harm, err := arms.NewHybridArm(arms.NewGrepArmWithRG(root, rgPath), marm)
		if err != nil {
			rec.Status = "mcp_stdio_failure"
			rec.GradeErr = err.Error()
			return rec
		}
		exec = harm
	default:
		exec = arms.NewGrepArmWithRG(root, rgPath)
	}

	cfg := agent.Config{
		Model: p.TaskModel, MaxTokens: 10000, Temperature: 0,
		MaxTurns: p.MaxTurns, MaxRetries: 8, TokenBudget: p.TokenBudget,
		RetryWait: 5 * time.Second,
	}
	res := agent.Run(ctx, client, cfg, exec, system, task.Intent)

	rec.Status = res.Status
	if res.ErrorDetail != "" {
		rec.GradeErr = res.ErrorDetail
	}
	rec.FinalAnswer = res.FinalAnswer
	rec.InputTokens = res.TotalInTokens
	rec.OutputTokens = res.TotalOutTokens
	rec.ToolCalls = res.ToolCallCount
	rec.Turns = res.TurnsToAnswer
	rec.WallClockMS = res.WallClockMillis
	rec.Retries = res.Retries

	// All LoCoBench tasks are edit-type → blind rubric judge.
	if res.FinalAnswer != "" {
		rs, err := judge.Score(ctx, task, key, res.FinalAnswer)
		if err != nil {
			rec.GradeErr = err.Error()
		} else {
			rec.Rubric = rs
		}
	}

	rec.TranscriptFile = writeTranscript(outDir, task.ID, arm, res)
	return rec
}

func playbookFor(arm runlog.Arm) string {
	switch arm {
	case runlog.ArmMaxContext:
		return arms.MCPlaybook
	case runlog.ArmHybrid:
		return arms.HybridPlaybook
	default:
		return arms.GrepPlaybook
	}
}

func indexRepo(mcBin, root string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, mcBin, "--reindex")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}

func writeTranscript(outDir, taskID string, arm runlog.Arm, res *agent.Result) string {
	name := fmt.Sprintf("%s_%s.json", taskID, arm)
	path := filepath.Join(outDir, "transcripts", name)
	b, _ := json.MarshalIndent(res, "", "  ")
	_ = os.WriteFile(path, b, 0o644)
	return filepath.Join("transcripts", name)
}

func writeResults(outDir string, r runlog.Results) error {
	b, _ := json.MarshalIndent(r, "", "  ")
	return os.WriteFile(filepath.Join(outDir, "results.json"), b, 0o644)
}

func rgVersionAt(rgPath string) string {
	out, err := exec.Command(rgPath, "--version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
}

func scoreBrief(r runlog.RunRecord) string {
	if r.Rubric != nil {
		return fmt.Sprintf("%d/%d", r.Rubric.Total, r.Rubric.MaxTotal)
	}
	if r.GradeErr != "" {
		return "gradeerr"
	}
	return "-"
}

// keep applies id-allowlist + language/category/difficulty filters to a scenario.
func (f filter) keep(s *scenario.Scenario) bool {
	if f.ids != nil && !f.ids[s.ID] {
		return false
	}
	if len(f.langs) > 0 {
		prefix, _ := s.ProjectPrefix()
		lang := strings.SplitN(prefix, "_", 2)[0]
		if !contains(f.langs, lang) {
			return false
		}
	}
	if len(f.cats) > 0 && !contains(f.cats, s.TaskCategory) {
		return false
	}
	if len(f.diffs) > 0 && !contains(f.diffs, s.Difficulty) {
		return false
	}
	return true
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

func indexOf(tasks []spec.Task, id string) int {
	for i, t := range tasks {
		if t.ID == id {
			return i
		}
	}
	return 0
}
