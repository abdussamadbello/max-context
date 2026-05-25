package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
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

// runIndex performs full index and optionally starts the watcher (handled in indexer).
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
	if err := indexer.Index(ctx, cfg.ProjectRoot, database, q); err != nil {
		return err
	}
	dir := filepath.Join(cfg.ProjectRoot, ".max-context")
	_ = artifacts.WriteSummary(dir, database)
	_ = artifacts.WriteArchitecture(dir, database)
	var totalFuncs, totalFiles int
	_ = database.QueryRow("SELECT COUNT(*) FROM functions").Scan(&totalFuncs)
	_ = database.QueryRow("SELECT COUNT(DISTINCT file_path) FROM functions").Scan(&totalFiles)
	_ = artifacts.WriteStatus(dir, &artifacts.Status{
		Healthy: true, LastFullIndex: time.Now(), TotalFunctions: totalFuncs, TotalFiles: totalFiles, Version: version,
	})
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
	return nil
}

// runWatch starts only the file watcher (no MCP server); blocks until process exits.
func runWatch(cfg *config.Config) error {
	reindexCh := make(chan string, 100)
	w, err := watcher.New(cfg.ProjectRoot, reindexCh)
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
	ctx := context.Background()
	go indexer.RunWorker(ctx, cfg.ProjectRoot, database, q, reindexCh)
	if watcher, err := watcher.New(cfg.ProjectRoot, reindexCh); err == nil {
		_ = watcher.Start(ctx)
	}

	handler := mcp.NewHandler()
	schemas := tools.RegisterAll(handler, database, q, cfg.ProjectRoot)
	srv := mcp.NewServer(handler, schemas)
	srv.SetProjectRoot(cfg.ProjectRoot) // Phase 6: MCP resources (summary, architecture)
	return srv.Serve()
}

func runSetup(cfg *config.Config, target string) error {
	return setup.Run(cfg.ProjectRoot, target)
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
	res, err := bench.Run(*repoFlag, questions, bench.RunOptions{
		OutDir: *outFlag,
		Repo:   repoName,
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
