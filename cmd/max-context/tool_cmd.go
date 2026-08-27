package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/maxcontext/max-context/internal/config"
	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/mcp"
	"github.com/maxcontext/max-context/internal/tools"
)

// runTool executes one MCP tool directly from the CLI and prints its JSON
// response to stdout — no MCP server, no watcher, no background worker. This
// gives shell scripts, hooks, and code-executing agents the same answers as
// the MCP tools at zero tool-definition context cost.
//
// Note: global flags (-project, -db) must precede the subcommand, e.g.
// `max-context -project /repo query "resolver cache"`. Subcommand flags may go
// on either side of the positional argument.
func runTool(cfg *config.Config, toolName string, argsJSON json.RawMessage) error {
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

	handler := mcp.NewHandler()
	tools.RegisterAll(handler, database, q, cfg.ProjectRoot)

	result, err := handler.Call(toolName, argsJSON)
	if err != nil {
		if rpcErr, ok := err.(*mcp.RPCError); ok && rpcErr.Code == mcp.CodeIndexNotReady {
			return fmt.Errorf("%s (build the index with `max-context --index` first)", rpcErr.Message)
		}
		return err
	}
	content, _ := result.([]mcp.ContentItem)
	for _, item := range content {
		fmt.Fprintln(os.Stdout, item.Text)
	}
	return nil
}

// runToolGeneric handles `max-context tool <name> --json '<args>'`.
func runToolGeneric(cfg *config.Config, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: max-context tool <name> [--json '<arguments>']")
	}
	name := args[0]
	fs := flag.NewFlagSet("tool", flag.ContinueOnError)
	argsFlag := fs.String("json", "{}", "tool arguments as a JSON object")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if !json.Valid([]byte(*argsFlag)) {
		return fmt.Errorf("--json is not valid JSON: %s", *argsFlag)
	}
	return runTool(cfg, name, json.RawMessage(*argsFlag))
}

// marshalArgs builds the tool argument payload from CLI flag values.
func marshalArgs(m map[string]interface{}) json.RawMessage {
	b, _ := json.Marshal(m)
	return b
}

// isBoolFlag reports whether f is a boolean flag, which takes no separate value.
func isBoolFlag(f *flag.Flag) bool {
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && bf.IsBoolFlag()
}

// parseFlagsAnywhere parses args allowing flags to appear before or after the
// positional arguments.
//
// The standard flag package stops at the first non-flag argument, so the natural
// `max-context calls Migrate -direction callers` silently ignored everything
// after "Migrate" and ran with the defaults — no error, just quietly wrong
// results. Here the two groups are partitioned first, then handed to Parse in
// the order it expects, so both orderings behave identically.
//
// A literal "--" ends flag parsing: everything after it is positional.
func parseFlagsAnywhere(fs *flag.FlagSet, args []string) error {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue // value supplied inline as -name=value
		}
		// A non-boolean flag consumes the next argument as its value.
		if f := fs.Lookup(name); f != nil && !isBoolFlag(f) && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	// Re-insert the terminator so a positional that looks like a flag (a symbol
	// starting with "-", or anything the caller put after their own "--") is not
	// re-interpreted as one.
	combined := append(flags, "--")
	return fs.Parse(append(combined, positional...))
}

// runQueryCmd handles `max-context query <text> [-n N] [-scope S] [-file-filter G]`.
func runQueryCmd(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	n := fs.Int("n", 3, "max results (1-50)")
	scope := fs.String("scope", "all", "all|functions|types|files|docs")
	fileFilter := fs.String("file-filter", "", "glob to filter by file path")
	if err := parseFlagsAnywhere(fs, args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: max-context query [flags] <text>")
	}
	a := map[string]interface{}{"query": fs.Arg(0), "max_results": *n, "scope": *scope}
	if *fileFilter != "" {
		a["file_filter"] = *fileFilter
	}
	return runTool(cfg, "query_codebase", marshalArgs(a))
}

// runDefCmd handles `max-context def <symbol>`.
func runDefCmd(cfg *config.Config, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: max-context def <symbol>")
	}
	return runTool(cfg, "get_definition", marshalArgs(map[string]interface{}{"symbol": args[0]}))
}

// runCallsCmd handles `max-context calls <function> [-direction D] [-depth N] [-min-confidence C]`.
func runCallsCmd(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("calls", flag.ContinueOnError)
	direction := fs.String("direction", "both", "callers|callees|both")
	depth := fs.Int("depth", 2, "max recursion depth (1-5)")
	minConfidence := fs.String("min-confidence", "", "minimum call-edge resolution confidence")
	if err := parseFlagsAnywhere(fs, args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: max-context calls [flags] <function>")
	}
	a := map[string]interface{}{"function_name": fs.Arg(0), "direction": *direction, "depth": *depth}
	if *minConfidence != "" {
		a["min_confidence"] = *minConfidence
	}
	return runTool(cfg, "get_call_chain", marshalArgs(a))
}

// runImpactCmd handles `max-context impact [-from-git REV] [-depth N] [-direction D] [-budget N] [files...]`.
func runImpactCmd(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("impact", flag.ContinueOnError)
	fromGit := fs.String("from-git", "", "git revision (e.g. HEAD or main..HEAD)")
	depth := fs.Int("depth", 2, "max recursion depth (1-5)")
	direction := fs.String("direction", "callers", "callers|callees|both")
	includeTests := fs.Bool("include-tests", true, "include test files in results")
	minConfidence := fs.String("min-confidence", "", "minimum call-edge resolution confidence")
	budget := fs.Int("budget", 0, "hard response budget in cl100k_base tokens (0 = uncapped)")
	if err := parseFlagsAnywhere(fs, args); err != nil {
		return err
	}
	if *budget < 0 {
		return fmt.Errorf("-budget must be non-negative")
	}
	a := map[string]interface{}{"depth": *depth, "direction": *direction, "include_tests": *includeTests}
	if fs.NArg() > 0 {
		a["files"] = fs.Args()
	} else if *fromGit != "" {
		a["from_git"] = *fromGit
	}
	if *minConfidence != "" {
		a["min_confidence"] = *minConfidence
	}
	if *budget > 0 {
		a["token_budget"] = *budget
	}
	return runTool(cfg, "get_impact", marshalArgs(a))
}

// runArchCmd handles `max-context arch [-focus subsystem]`.
func runArchCmd(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("arch", flag.ContinueOnError)
	focus := fs.String("focus", "", "optional subsystem filter")
	if err := parseFlagsAnywhere(fs, args); err != nil {
		return err
	}
	a := map[string]interface{}{}
	if *focus != "" {
		a["focus"] = *focus
	}
	return runTool(cfg, "get_architecture", marshalArgs(a))
}
