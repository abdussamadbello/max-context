package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/maxcontext/max-context/internal/config"
	"github.com/maxcontext/max-context/internal/contextcompiler"
	"github.com/maxcontext/max-context/internal/db"
)

type repeatedStrings []string

func (s *repeatedStrings) String() string { return strings.Join(*s, ",") }
func (s *repeatedStrings) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("changed file path cannot be empty")
	}
	*s = append(*s, value)
	return nil
}

// runContextCmd implements the experimental CLI-only context compiler. Keeping
// it out of RegisterAll means it adds no permanent MCP schema overhead while its
// ranking and session economics are evaluated.
func runContextCmd(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("context", flag.ContinueOnError)
	taskFlag := fs.String("task", "", "task to compile context for (positional text is also accepted)")
	budget := fs.Int("budget", 4000, "hard final-response budget in cl100k_base tokens")
	intent := fs.String("intent", contextcompiler.IntentAuto, "auto|locate|understand|change|debug|impact|test|review")
	maxDepth := fs.Int("max-depth", 2, "maximum call/impact depth (1-5)")
	var changedFiles repeatedStrings
	fs.Var(&changedFiles, "changed-file", "project-relative changed file (repeatable)")
	if err := parseFlagsAnywhere(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 && strings.TrimSpace(*taskFlag) == "" {
		return fmt.Errorf("usage: max-context context [flags] --task text\n       max-context context [flags] <task>")
	}
	if fs.NArg() > 0 && strings.TrimSpace(*taskFlag) != "" {
		return fmt.Errorf("provide the task either with -task or as positional text, not both")
	}
	task := strings.TrimSpace(*taskFlag)
	if task == "" {
		task = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	if task == "" {
		return fmt.Errorf("task is required")
	}

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

	_, payload, err := contextcompiler.Compile(context.Background(), database, q, cfg.ProjectRoot,
		compileOptions(cfg, task, *intent, *budget, *maxDepth, changedFiles))
	if err != nil {
		return fmt.Errorf("compile context: %w", err)
	}
	_, err = os.Stdout.Write(payload)
	return err
}

// compileOptions builds compiler options from project config plus the per-call
// knobs. Shared by the subcommand and the benchmark so the benchmark measures
// the shipped command rather than a reimplementation that can drift from it.
func compileOptions(cfg *config.Config, task, intent string, budget, maxDepth int, changed []string) contextcompiler.Options {
	return contextcompiler.Options{
		Task:         task,
		TokenBudget:  budget,
		Intent:       intent,
		ChangedFiles: changed,
		MaxDepth:     maxDepth,
		Extensions:   cfg.LanguageExtensions(),
		Include:      cfg.IncludeGlobs(),
		Exclude:      cfg.ExcludeGlobs(),
		MaxFileSize:  cfg.EffectiveMaxFileSize(),
	}
}

// benchContextArgs is the mc_args shape for a benchmark question whose mc_tool
// is "context". Budget is explicit per question: the whole measurement is what
// a package costs at a chosen budget, so defaulting it silently would hide the
// one number the question exists to report.
type benchContextArgs struct {
	Task     string `json:"task"`
	Budget   int    `json:"budget"`
	Intent   string `json:"intent"`
	MaxDepth int    `json:"max_depth"`
}

// compileContextForBench runs the compiler for a benchmark question.
//
// The benchmark dispatches tools through the MCP handler, and `context` is
// deliberately absent from RegisterAll — costing no schema is the point of it.
// Routing it here keeps it measurable against the same index without buying it
// a permanent per-turn cost it has not yet earned.
func compileContextForBench(cfg *config.Config, database *sql.DB, q *db.Queries, raw json.RawMessage) (string, error) {
	var in benchContextArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", fmt.Errorf("parse context mc_args: %w", err)
		}
	}
	if strings.TrimSpace(in.Task) == "" {
		return "", fmt.Errorf(`context questions need a non-empty "task" in mc_args`)
	}
	if in.Budget <= 0 {
		return "", fmt.Errorf(`context question %q needs an explicit positive "budget" in mc_args`, in.Task)
	}
	if in.MaxDepth == 0 {
		in.MaxDepth = 2
	}
	if in.Intent == "" {
		in.Intent = contextcompiler.IntentAuto
	}
	_, payload, err := contextcompiler.Compile(context.Background(), database, q, cfg.ProjectRoot,
		compileOptions(cfg, in.Task, in.Intent, in.Budget, in.MaxDepth, nil))
	if err != nil {
		return "", err
	}
	return string(payload), nil
}
