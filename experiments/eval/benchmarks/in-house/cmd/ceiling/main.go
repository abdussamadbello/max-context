// Command ceiling runs the retrieval-ceiling A/B with no model in the loop.
//
//	go run ./cmd/ceiling
//
// No API key, no Bedrock, no cost. It builds the max-context binary from the
// parent repo if one is not supplied, indexes each probe repo, runs both arms'
// declared tool calls, and scores what came back against the hand-verified gold
// sets. Remote-repo probes are skipped unless -remote is passed, and the skip is
// reported rather than silently narrowing coverage.
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

	"github.com/maxcontext/eval/internal/ceiling"
	"github.com/maxcontext/eval/internal/spec"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ceiling: %v\n", err)
		os.Exit(1)
	}
}

type patternList []string

func (p *patternList) String() string     { return strings.Join(*p, ",") }
func (p *patternList) Set(v string) error { *p = append(*p, v); return nil }

func run() error {
	var extra patternList
	protocolPath := flag.String("protocol", "protocol/ceiling-v1.json", "ceiling protocol JSON")
	keysPath := flag.String("keys", "protocol/alias-v4.json", "agent protocol whose answer keys the gold sets must match")
	mcBinary := flag.String("mc", "", "path to the max-context binary (default: build it from the parent repo)")
	rgPath := flag.String("rg", "rg", "path to the ripgrep binary")
	outDir := flag.String("out", "results/ceiling", "output directory")
	remote := flag.Bool("remote", false, "also run probes that must clone a remote repo (needs network)")
	flag.Var(&extra, "grep-pattern", "extra ripgrep pattern for the baseline arm (repeatable) — add your own and re-run")
	flag.Parse()

	cp, err := ceiling.Load(*protocolPath)
	if err != nil {
		return err
	}
	// The gold sets live in two files; refuse to run if they have diverged.
	keys, err := spec.Load(*keysPath)
	if err != nil {
		return fmt.Errorf("load answer keys: %w", err)
	}
	if err := ceiling.VerifyAgainstKeys(cp, keys); err != nil {
		return err
	}

	binary := *mcBinary
	if binary == "" {
		binary, err = buildMaxContext()
		if err != nil {
			return err
		}
	}
	if _, err := exec.LookPath(*rgPath); err != nil {
		return fmt.Errorf("ripgrep not found at %q: %w (the baseline arm needs it; pass -rg <path>)", *rgPath, err)
	}

	ctx := context.Background()
	res := &ceiling.Results{Version: cp.Version}

	for _, probe := range cp.Probes {
		pr := ceiling.ProbeResult{
			TaskID: probe.TaskID, Repo: probe.Repo,
			Question: probe.Question, Symbol: probe.Symbol, Gold: probe.ExpectedSymbols,
		}
		if probe.NeedsNetwork() && !*remote {
			pr.Skipped = "needs a network clone; re-run with -remote to include it"
			res.Skipped = append(res.Skipped, probe.TaskID+" ("+probe.Repo+"): "+pr.Skipped)
			res.Probes = append(res.Probes, pr)
			continue
		}

		work, cleanup, err := materialize(ctx, probe)
		if err != nil {
			return fmt.Errorf("probe %s: %w", probe.TaskID, err)
		}

		pr.Arms = []ceiling.ArmResult{
			ceiling.RunGrep(ctx, work, probe, extra, *rgPath, false),
			ceiling.RunGrep(ctx, work, probe, extra, *rgPath, true),
			ceiling.RunMaxContext(ctx, work, probe, binary),
		}
		cleanup()
		res.Probes = append(res.Probes, pr)
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}
	body, _ := json.MarshalIndent(res, "", "  ")
	if err := os.WriteFile(filepath.Join(*outDir, "results.json"), body, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*outDir, "ceiling.md"), []byte(ceiling.Render(res)), 0o644); err != nil {
		return err
	}

	fmt.Print(ceiling.Summary(res))
	fmt.Printf("\nWrote %s\n", filepath.Join(*outDir, "ceiling.md"))
	return nil
}

// materialize puts the probe's repo in a scratch directory. Indexing writes
// .max-context/ into the tree, so committed fixtures are copied, never indexed
// in place.
func materialize(ctx context.Context, p ceiling.Probe) (string, func(), error) {
	dir, err := os.MkdirTemp("", "ceiling-"+p.Repo+"-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }

	if p.RepoPath != "" {
		if _, err := os.Stat(p.RepoPath); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("fixture %s is missing: %w", p.RepoPath, err)
		}
		if err := ceiling.CopyDir(p.RepoPath, dir); err != nil {
			cleanup()
			return "", nil, err
		}
		return dir, cleanup, nil
	}

	clone := exec.CommandContext(ctx, "git", "clone", "--quiet", p.CloneURL, dir)
	if out, err := clone.CombinedOutput(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("clone %s: %v: %s", p.CloneURL, err, strings.TrimSpace(string(out)))
	}
	if p.SHA != "" {
		co := exec.CommandContext(ctx, "git", "-C", dir, "checkout", "--quiet", p.SHA)
		if out, err := co.CombinedOutput(); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("checkout %s: %v: %s", p.SHA, err, strings.TrimSpace(string(out)))
		}
	}
	return dir, cleanup, nil
}

// buildMaxContext compiles the server from the enclosing repo so the command
// works from a clean checkout with nothing installed.
func buildMaxContext() (string, error) {
	root, err := repoRoot()
	if err != nil {
		return "", err
	}
	out := filepath.Join(os.TempDir(), "max-context-ceiling")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/max-context")
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build max-context: %v: %s", err, strings.TrimSpace(string(b)))
	}
	return out, nil
}

// repoRoot walks up from the working directory looking for the max-context
// module, which is the parent of this eval module.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(b), "module github.com/maxcontext/max-context") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find the max-context repo above %q; pass -mc <path-to-binary>", dir)
		}
		dir = parent
	}
}
