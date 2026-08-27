package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maxcontext/max-context/internal/artifacts"
	"github.com/maxcontext/max-context/internal/bench"
	"github.com/maxcontext/max-context/internal/config"
	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/indexer"
	"github.com/maxcontext/max-context/internal/mcp"
	"github.com/maxcontext/max-context/internal/setup"
	"github.com/maxcontext/max-context/internal/tools"
	"github.com/maxcontext/max-context/internal/watcher"
)

var version = "0.1.0" // set by ldflags in release builds

func main() {
	cfg := config.Flags{}
	cfg.Register(flag.CommandLine)

	flag.Parse()

	if cfg.Version {
		fmt.Fprintf(os.Stdout, "max-context %s\n", version)
		os.Exit(0)
	}

	// Load merged config (flags + .max-context/config.json if present)
	merged, err := config.Load(cfg.Project, &cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	// Dispatch to subcommands or MCP server
	if err := run(merged); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

// run dispatches based on config: setup subcommand, --index, --reindex, --status, --watch, or MCP server mode.
func run(cfg *config.Config) error {
	args := flag.Args()
	if len(args) >= 1 && args[0] == "setup" {
		target := "all"
		if len(args) >= 2 {
			target = args[1]
		}
		return runSetup(cfg, target)
	}
	if len(args) >= 1 && args[0] == "bench" {
		return runBench(cfg, args[1:])
	}
	// CLI access to the MCP tools (one-shot, read-only; JSON to stdout).
	if len(args) >= 1 {
		switch args[0] {
		case "tool":
			return runToolGeneric(cfg, args[1:])
		case "query":
			return runQueryCmd(cfg, args[1:])
		case "def":
			return runDefCmd(cfg, args[1:])
		case "calls":
			return runCallsCmd(cfg, args[1:])
		case "impact":
			return runImpactCmd(cfg, args[1:])
		case "context":
			return runContextCmd(cfg, args[1:])
		case "arch":
			return runArchCmd(cfg, args[1:])
		}
	}
	switch {
	case cfg.Index:
		return runIndex(cfg)
	case cfg.Reindex:
		return runReindex(cfg)
	case cfg.Status:
		return runStatus(cfg)
	case cfg.Watch:
		return runWatch(cfg)
	default:
		return runMCPServer(cfg)
	}
}

// indexerOptions maps the merged config onto indexing options.
func indexerOptions(cfg *config.Config) *indexer.Options {
	return &indexer.Options{
		Extensions:  cfg.LanguageExtensions(),
		Include:     cfg.IncludeGlobs(),
		Exclude:     cfg.ExcludeGlobs(),
		MaxFileSize: cfg.EffectiveMaxFileSize(),
		Version:     version,
	}
}

// watcherOptions maps the merged config onto watcher options.
func watcherOptions(cfg *config.Config) *watcher.Options {
	return &watcher.Options{
		DebounceMs: cfg.EffectiveDebounceMs(),
		Extensions: cfg.LanguageExtensions(),
	}
}

// runIndex builds the full index and exits. It does not start the watcher —
// that runs in MCP server mode (runServe) and under --watch.
func runIndex(cfg *config.Config) error {
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		return err
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		return err
	}
	defer q.Close()
	ctx := context.Background()
	dir := filepath.Join(cfg.ProjectRoot, ".max-context")
	if err := indexer.Index(ctx, cfg.ProjectRoot, database, q, indexerOptions(cfg)); err != nil {
		artifacts.RecordIndexError(database, "", err.Error())
		_ = artifacts.WriteIndexStatus(dir, database, version)
		return err
	}
	// A clean full index clears any prior per-file failures and stamps the time.
	artifacts.ClearErrorsAfterFullIndex(database)
	artifacts.SetLastFullIndex(database, time.Now())
	artifacts.ClearIndexError(database, artifacts.ArtifactErrorKey)
	if err := artifacts.WriteIndexArtifacts(dir, database, version); err != nil {
		artifacts.RecordIndexError(database, artifacts.ArtifactErrorKey, err.Error())
		_ = artifacts.WriteIndexStatus(dir, database, version)
		return err
	}
	fmt.Fprintf(os.Stdout, "Index complete.\n")
	return nil
}

// runReindex forces a full rebuild of the index.
func runReindex(cfg *config.Config) error {
	return runIndex(cfg)
}

// runStatus prints index health, staleness, coverage to stdout.
func runStatus(cfg *config.Config) error {
	dir := filepath.Join(cfg.ProjectRoot, ".max-context")
	s, err := artifacts.ReadStatus(dir)
	if err != nil {
		fmt.Fprintf(os.Stdout, "status: no index (%v)\n", err)
		return nil
	}
	fmt.Fprintf(os.Stdout, "healthy: %v\nfunctions: %d\nfiles: %d\nlast_full: %v\n",
		s.Healthy, s.TotalFunctions, s.TotalFiles, s.LastFullIndex)

	// Pull live staleness from the DB so failed-file counts and the last
	// incremental update show up even when status.json predates them.
	database, derr := db.Open(cfg.DBPath)
	if derr != nil {
		return nil
	}
	defer database.Close()
	h := artifacts.ReadIndexHealth(database)
	if !h.LastIncrementalIndex.IsZero() {
		fmt.Fprintf(os.Stdout, "last_incremental: %v\n", h.LastIncrementalIndex)
	}
	fmt.Fprintf(os.Stdout, "failed_files: %d\n", h.FailedFiles)
	if h.FailedFiles > 0 {
		fmt.Fprintf(os.Stdout, "WARNING: %d file(s) failed to index since the last clean build; results may be stale — run --reindex.\n", h.FailedFiles)
		rows, qerr := database.Query("SELECT file_path, error FROM index_errors ORDER BY file_path")
		if qerr == nil {
			defer rows.Close()
			for rows.Next() {
				var fp, msg string
				if rows.Scan(&fp, &msg) == nil {
					fmt.Fprintf(os.Stdout, "  - %s: %s\n", fp, msg)
				}
			}
		}
	}
	return nil
}

// runWatch starts only the file watcher (no MCP server); blocks until process exits.
func runWatch(cfg *config.Config) error {
	reindexCh := make(chan string, 100)
	opts := watcherOptions(cfg)
	opts.OnError = func(err error) {
		fmt.Fprintf(os.Stderr, "max-context: watcher error: %v\n", err)
	}
	w, err := watcher.New(cfg.ProjectRoot, reindexCh, opts)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if err := w.Start(ctx); err != nil {
		return err
	}
	select {}
}

// runMCPServer runs the MCP server on stdin/stdout (long-lived).
func runMCPServer(cfg *config.Config) error {
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		return fmt.Errorf("prepare queries: %w", err)
	}
	defer q.Close()

	reindexCh := make(chan string, 100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go indexer.RunWorker(ctx, cfg.ProjectRoot, database, q, reindexCh, indexerOptions(cfg))
	if _, err := startProjectWatcher(ctx, cfg, database, reindexCh); err != nil {
		// Keep serving the existing index, but make degraded freshness explicit in
		// stderr and in every tool's staleness payload.
		fmt.Fprintf(os.Stderr, "max-context: watcher unavailable: %v\n", err)
	}

	handler := mcp.NewHandler()
	schemas := tools.RegisterAll(handler, database, q, cfg.ProjectRoot)
	srv := mcp.NewServer(handler, schemas)
	srv.SetProjectRoot(cfg.ProjectRoot) // Phase 6: MCP resources (summary, architecture)
	return srv.Serve()
}

func startProjectWatcher(ctx context.Context, cfg *config.Config, database *sql.DB, reindexCh chan<- string) (*watcher.Watcher, error) {
	opts := watcherOptions(cfg)
	opts.OnError = func(err error) {
		fmt.Fprintf(os.Stderr, "max-context: watcher error: %v\n", err)
		artifacts.RecordIndexError(database, artifacts.WatcherErrorKey, err.Error())
	}
	w, err := watcher.New(cfg.ProjectRoot, reindexCh, opts)
	if err != nil {
		artifacts.RecordIndexError(database, artifacts.WatcherErrorKey, err.Error())
		return nil, err
	}
	if err := w.Start(ctx); err != nil {
		artifacts.RecordIndexError(database, artifacts.WatcherErrorKey, err.Error())
		return nil, err
	}
	artifacts.ClearIndexError(database, artifacts.WatcherErrorKey)
	return w, nil
}

func runSetup(cfg *config.Config, target string) error {
	report, err := setup.Run(cfg.ProjectRoot, target)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Configured %s in %s:\n%s", target, cfg.ProjectRoot, report)
	if skipped := report.Skipped(); len(skipped) > 0 {
		fmt.Fprintf(os.Stdout, "\n%d file(s) needed manual attention — see SKIPPED above.\n", len(skipped))
	}
	for _, note := range report.Notes {
		fmt.Fprintf(os.Stdout, "\nNote: %s\n", note)
	}
	fmt.Fprintf(os.Stdout, "\nNext: run `max-context --index` in this project, then start your editor.\n")
	return nil
}

// runBench executes the benchmark harness against a question set and writes
// results.json + benchmark.md to the output directory.
func runBench(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	repoFlag := fs.String("repo", cfg.ProjectRoot, "repo root to benchmark (defaults to project root)")
	questionsFlag := fs.String("questions", "", "path to questions JSON (defaults to benchmark/questions/<repo-name>.json)")
	outFlag := fs.String("out", "benchmark", "output directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	repoName := filepath.Base(*repoFlag)
	qPath := *questionsFlag
	if qPath == "" {
		qPath = filepath.Join("benchmark", "questions", repoName+".json")
	}
	body, err := os.ReadFile(qPath)
	if err != nil {
		return fmt.Errorf("read questions: %w", err)
	}
	var questions []bench.Question
	if err := json.Unmarshal(body, &questions); err != nil {
		return fmt.Errorf("parse questions: %w", err)
	}
	// The max-context side of the benchmark is measured by invoking the real
	// tools against the real index, exactly as the baselines run real greps.
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open db (build the index with `max-context --index` first): %w", err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		return err
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		return err
	}
	defer q.Close()
	handler := mcp.NewHandler()
	tools.RegisterAll(handler, database, q, cfg.ProjectRoot)

	invoke := func(tool string, args json.RawMessage) (string, error) {
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		result, err := handler.Call(tool, args)
		if err != nil {
			return "", err
		}
		content, _ := result.([]mcp.ContentItem)
		var b strings.Builder
		for _, item := range content {
			b.WriteString(item.Text)
		}
		return b.String(), nil
	}

	res, err := bench.Run(*repoFlag, questions, bench.RunOptions{
		OutDir:     *outFlag,
		Repo:       repoName,
		InvokeTool: invoke,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Benchmarked %d questions on %q. Naive savings: %.1fx, Skilled: %.1fx.\nResults: %s\n",
		res.Summary.QuestionCount, res.Repo,
		res.Summary.NaiveSavingsX, res.Summary.SkilledSavingsX,
		filepath.Join(*outFlag, "results.json"),
	)
	return nil
}
