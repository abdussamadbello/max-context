// Command eval runs the causal A/B experiment: for each task, the same model
// answers once with grep tools (Arm A) and once with max-context over stdio MCP
// (Arm B). Retrieval is graded objectively; edit tasks via a blind, different-
// model judge. The shipped core binary is never linked here — this is a
// separate module, so max-context stays LLM-free.
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

	"github.com/maxcontext/eval/internal/agent"
	"github.com/maxcontext/eval/internal/arms"
	"github.com/maxcontext/eval/internal/grade"
	"github.com/maxcontext/eval/internal/runlog"
	"github.com/maxcontext/eval/internal/spec"
)

type backendOpts struct {
	kind        string // "anthropic" | "bedrock"
	awsRegion   string
	awsProfile  string
	modelPrefix string // prepended to protocol model ids (e.g. "us.anthropic." for Bedrock)
	// Full per-model overrides (take precedence over modelPrefix). Bedrock IDs are
	// inconsistent — sonnet works unversioned, haiku needs the version suffix — so
	// a single prefix can't cover both. These let each model be set explicitly.
	taskModel  string
	judgeModel string
}

func main() {
	var (
		protocolPath = flag.String("protocol", "protocol/httpx.json", "path to protocol JSON")
		workDir      = flag.String("work", "/tmp/eval-repos", "dir to clone repos into")
		outDir       = flag.String("out", "results", "output dir")
		mcBin        = flag.String("mc-bin", "max-context", "path to max-context binary")
		rgPath       = flag.String("rg", "rg", "path to the ripgrep binary (use an absolute path if 'rg' is a shell function, not a real binary)")
		backend      = flag.String("backend", "anthropic", "model backend: anthropic | bedrock")
		awsRegion    = flag.String("aws-region", "us-east-1", "AWS region (bedrock backend)")
		awsProfile   = flag.String("aws-profile", "", "AWS profile (bedrock backend)")
		modelPrefix  = flag.String("model-prefix", "", "prefix prepended to protocol model ids (e.g. 'us.anthropic.' for Bedrock)")
		taskModel    = flag.String("task-model", "", "override the task model id entirely (e.g. a full Bedrock profile id)")
		judgeModel   = flag.String("judge-model", "", "override the judge model id entirely")
		dryRun       = flag.Bool("dry-run", false, "validate protocol + clone/index, skip LLM calls")
	)
	flag.Parse()

	bo := backendOpts{kind: *backend, awsRegion: *awsRegion, awsProfile: *awsProfile, modelPrefix: *modelPrefix, taskModel: *taskModel, judgeModel: *judgeModel}
	if err := run(*protocolPath, *workDir, *outDir, *mcBin, *rgPath, bo, *dryRun); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(protocolPath, workDir, outDir, mcBin, rgPath string, bo backendOpts, dryRun bool) error {
	p, err := spec.Load(protocolPath)
	if err != nil {
		return err
	}
	hash := p.Hash()
	fmt.Printf("protocol hash (pre-registration): %s\n", hash)

	// Resolve backend model ids: explicit overrides win; otherwise apply the
	// prefix. (Bedrock ids are inconsistent across models, so overrides exist.)
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
		if apiKey == "" && !dryRun {
			return fmt.Errorf("ANTHROPIC_API_KEY not set (use --dry-run, or --backend bedrock)")
		}
		client = agent.NewClient(apiKey)
	}
	judge := grade.NewJudge(client, p.JudgeModel)

	// Preflight the grep arm: abort loudly if ripgrep can't execute, so a broken
	// baseline can never silently lose every task and fake a max-context win.
	preflightCtx, cancelPF := context.WithTimeout(context.Background(), 30*time.Second)
	if err := arms.NewGrepArmWithRG(workDir, rgPath).Preflight(preflightCtx); err != nil {
		cancelPF()
		return fmt.Errorf("grep arm preflight failed: %w", err)
	}
	cancelPF()

	if err := os.MkdirAll(filepath.Join(outDir, "transcripts"), 0o755); err != nil {
		return err
	}

	manifest := runlog.Manifest{
		ProtocolHash: hash, ProtocolFile: protocolPath,
		TaskModel: p.TaskModel, JudgeModel: p.JudgeModel, Temperature: 0,
		MaxTurns: p.MaxTurns, TokenBudget: p.TokenBudget, Replicates: max1(p.Replicates),
		RepoSHAs: map[string]string{}, IndexSHAs: map[string]string{},
		MaxContextBin: mcBin, RGVersion: rgVersionAt(rgPath), StartedAt: time.Now().UTC().Format(time.RFC3339),
		GrepPlaybook: arms.GrepPlaybook, MCPlaybook: arms.MCPlaybook,
	}

	// Clone + index each repo once.
	repoRoots := map[string]string{}
	for _, repo := range p.Repos {
		root := filepath.Join(workDir, repo.Name)
		sha, err := cloneAt(repo, root)
		if err != nil {
			return fmt.Errorf("clone %s: %w", repo.Name, err)
		}
		manifest.RepoSHAs[repo.Name] = sha
		if sha != repo.SHA {
			fmt.Printf("WARNING: %s checked out %s, protocol pinned %s\n", repo.Name, sha, repo.SHA)
		}
		if err := indexRepo(mcBin, root); err != nil {
			return fmt.Errorf("index %s: %w", repo.Name, err)
		}
		manifest.IndexSHAs[repo.Name] = sha // index built from this checkout
		repoRoots[repo.Name] = root
		fmt.Printf("prepared repo %s @ %s\n", repo.Name, sha[:12])
	}

	if dryRun {
		fmt.Println("dry-run: protocol valid, repos cloned + indexed, arms wired. Skipping LLM calls.")
		return writeResults(outDir, runlog.Results{Manifest: manifest})
	}

	var records []runlog.RunRecord
	reps := max1(p.Replicates)
	for _, task := range p.Tasks {
		root := repoRoots[task.Repo]
		key, _ := p.KeyFor(task.ID)
		for rep := 0; rep < reps; rep++ {
			phrasing := pickPhrasing(task, rep)
			// Arm A (grep) and Arm B (max-context) — paired, same phrasing.
			recA := runTask(client, judge, p, task, key, phrasing, runlog.ArmGrep, root, mcBin, rgPath, rep, outDir)
			records = append(records, recA)
			recB := runTask(client, judge, p, task, key, phrasing, runlog.ArmMaxContext, root, mcBin, rgPath, rep, outDir)
			records = append(records, recB)
			fmt.Printf("  %s rep%d: grep=%s/%s  mc=%s/%s\n", task.ID, rep,
				recA.Status, scoreBrief(recA), recB.Status, scoreBrief(recB))

			// Checkpoint after every task so a mid-run death (e.g. rate limit)
			// never loses completed work; the report can run on a partial file.
			if err := writeResults(outDir, runlog.Results{Manifest: manifest, Runs: records}); err != nil {
				return err
			}
			// Pacing: a brief gap between tasks eases provider rate limits on
			// low-tier keys. Per-call 429s are still retried with backoff.
			time.Sleep(pace)
		}
	}

	return writeResults(outDir, runlog.Results{Manifest: manifest, Runs: records})
}

// pace is the inter-task delay to stay under rate limits.
const pace = 5 * time.Second

// runTask executes one (task, arm, replicate), grades it, writes its transcript.
func runTask(client agent.Caller, judge *grade.Judge, p *spec.Protocol, task spec.Task, key spec.Key,
	phrasing string, arm runlog.Arm, root, mcBin, rgPath string, rep int, outDir string) runlog.RunRecord {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	rec := runlog.RunRecord{TaskID: task.ID, Repo: task.Repo, Arm: arm, Replicate: rep, Phrasing: phrasing}
	system := p.SystemSkeleton + "\n\n" + playbookFor(arm)

	var exec agent.ToolExecutor
	var mcpClient *arms.MCPClient
	if arm == runlog.ArmMaxContext {
		c, err := arms.StartMCP(ctx, mcBin, root)
		if err != nil {
			rec.Status = "mcp_stdio_failure"
			rec.GradeErr = err.Error()
			return rec
		}
		mcpClient = c
		defer mcpClient.Close()
		marm, err := arms.NewMCPArm(ctx, c)
		if err != nil {
			rec.Status = "mcp_stdio_failure"
			rec.GradeErr = err.Error()
			return rec
		}
		exec = marm
	} else {
		exec = arms.NewGrepArmWithRG(root, rgPath)
	}

	cfg := agent.Config{
		Model: p.TaskModel, MaxTokens: 10000, Temperature: 0,
		MaxTurns: p.MaxTurns, MaxRetries: 8, TokenBudget: p.TokenBudget,
		RetryWait: 5 * time.Second, // used only when no Retry-After header
	}
	res := agent.Run(ctx, client, cfg, exec, system, phrasing)

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

	// Grade by task type (objective first; judge only for edit).
	switch task.Type {
	case spec.TypeRetrieval:
		s := grade.GradeRetrieval(res.FinalAnswer, key)
		rec.Retrieval = &s
	case spec.TypeImpact:
		// Relationship tasks ("all callers of X", "blast radius of Y") are graded
		// by symbol-set recall — the metric where the call graph beats grep.
		s := grade.GradeSymbols(res.FinalAnswer, key)
		rec.Retrieval = &s
	case spec.TypeEdit:
		if res.FinalAnswer != "" {
			rs, err := judge.Score(ctx, task, key, res.FinalAnswer)
			if err != nil {
				rec.GradeErr = err.Error()
			} else {
				rec.Rubric = rs
			}
		}
	}

	rec.TranscriptFile = writeTranscript(outDir, task.ID, arm, rep, res)
	return rec
}

func playbookFor(arm runlog.Arm) string {
	if arm == runlog.ArmMaxContext {
		return arms.MCPlaybook
	}
	return arms.GrepPlaybook
}

// pickPhrasing rotates through the primary intent + paraphrases by replicate.
func pickPhrasing(task spec.Task, rep int) string {
	options := append([]string{task.Intent}, task.Paraphrases...)
	return options[rep%len(options)]
}

func writeTranscript(outDir, taskID string, arm runlog.Arm, rep int, res *agent.Result) string {
	name := fmt.Sprintf("%s_%s_r%d.json", taskID, arm, rep)
	path := filepath.Join(outDir, "transcripts", name)
	b, _ := json.MarshalIndent(res, "", "  ")
	_ = os.WriteFile(path, b, 0o644)
	return filepath.Join("transcripts", name)
}

func writeResults(outDir string, r runlog.Results) error {
	b, _ := json.MarshalIndent(r, "", "  ")
	return os.WriteFile(filepath.Join(outDir, "results.json"), b, 0o644)
}

// cloneAt shallow-clones repo into root (removing any prior clone) and returns
// the checked-out SHA. Pins to the protocol SHA by fetching it when possible.
func cloneAt(repo spec.Repo, root string) (string, error) {
	_ = os.RemoveAll(root)
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		return "", err
	}
	// Try to fetch the exact pinned SHA; fall back to default branch if the
	// host disallows fetch-by-sha (then we warn on mismatch upstream).
	if err := gitRun("", "init", "-q", root); err != nil {
		return "", err
	}
	if err := gitRun(root, "remote", "add", "origin", repo.CloneURL); err != nil {
		return "", err
	}
	if err := gitRun(root, "fetch", "-q", "--depth", "1", "origin", repo.SHA); err == nil {
		if err := gitRun(root, "checkout", "-q", "FETCH_HEAD"); err != nil {
			return "", err
		}
	} else {
		// Fallback: shallow clone default branch.
		_ = os.RemoveAll(root)
		if err := gitRun("", "clone", "-q", "--depth", "1", repo.CloneURL, root); err != nil {
			return "", err
		}
	}
	return gitOutput(root, "rev-parse", "HEAD")
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

func gitRun(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd.Run()
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func rgVersionAt(rgPath string) string {
	out, err := exec.Command(rgPath, "--version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
}

func scoreBrief(r runlog.RunRecord) string {
	if r.Retrieval != nil {
		return fmt.Sprintf("F1=%.2f", r.Retrieval.F1)
	}
	if r.Rubric != nil {
		return fmt.Sprintf("%d/%d", r.Rubric.Total, r.Rubric.MaxTotal)
	}
	if r.GradeErr != "" {
		return "gradeerr"
	}
	return "-"
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
