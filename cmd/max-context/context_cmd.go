package main

import (
	"context"
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

	_, payload, err := contextcompiler.Compile(context.Background(), database, q, cfg.ProjectRoot, contextcompiler.Options{
		Task:         task,
		TokenBudget:  *budget,
		Intent:       *intent,
		ChangedFiles: changedFiles,
		MaxDepth:     *maxDepth,
		Extensions:   cfg.LanguageExtensions(),
		Include:      cfg.IncludeGlobs(),
		Exclude:      cfg.ExcludeGlobs(),
		MaxFileSize:  cfg.EffectiveMaxFileSize(),
	})
	if err != nil {
		return fmt.Errorf("compile context: %w", err)
	}
	_, err = os.Stdout.Write(payload)
	return err
}
