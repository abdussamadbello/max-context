// Package arms provides the two experiment arms as ToolExecutors:
//   - GrepArm (baseline): real ripgrep + read_file + list_files
//   - MCPArm (max-context): the 4 real tools over stdio MCP
package arms

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/maxcontext/eval-locobench/internal/agent"
)

// GrepArm is the baseline arm. It exposes a genuinely capable toolset — real
// ripgrep with the full flag surface, ranged/whole file reads, and glob listing
// — so the comparison is against a *competent* grep agent, not a strawman. All
// access is confined to root.
type GrepArm struct {
	Root    string // repo root; all paths resolved/confined under here
	RGPath  string // path to rg binary (default "rg")
	MaxRead int    // max bytes returned by read_file (0 = 200_000)
}

// NewGrepArm builds a grep arm rooted at repoRoot using the default "rg".
func NewGrepArm(repoRoot string) *GrepArm {
	return NewGrepArmWithRG(repoRoot, "rg")
}

// NewGrepArmWithRG builds a grep arm with an explicit ripgrep path. Use this
// when "rg" is not a plain binary on PATH (e.g. a shell-function wrapper).
func NewGrepArmWithRG(repoRoot, rgPath string) *GrepArm {
	if rgPath == "" {
		rgPath = "rg"
	}
	return &GrepArm{Root: repoRoot, RGPath: rgPath, MaxRead: 200_000}
}

// Preflight verifies the grep arm can actually execute its core tool. It runs
// `rg --version` and a trivial search, returning an error if either fails. The
// orchestrator calls this BEFORE the experiment so a broken grep arm aborts the
// run loudly rather than silently losing every task (which would fake a
// max-context win — the exact confound the methodology guards against).
func (g *GrepArm) Preflight(ctx context.Context) error {
	if err := exec.CommandContext(ctx, g.RGPath, "--version").Run(); err != nil {
		return fmt.Errorf("ripgrep not executable at %q: %w (pass --rg <path-to-rg-binary>)", g.RGPath, err)
	}
	out, isErr := g.grep(ctx, json.RawMessage(`{"pattern":"."}`))
	if isErr {
		return fmt.Errorf("grep preflight search failed: %s", out)
	}
	return nil
}

func (g *GrepArm) Tools() []agent.Tool {
	return []agent.Tool{
		{
			Name: "grep",
			Description: "Search file contents with ripgrep across the repository. " +
				"Supports the full ripgrep flag surface — pass them in `args`. Useful flags: " +
				"-i (case-insensitive), -w (whole word), -F (fixed string, no regex), " +
				"-A/-B/-C N (lines of context after/before/around), -t LANG (filter by file type, e.g. -t py -t ts), " +
				"-g GLOB (include/exclude by glob, e.g. -g '!*_test.py'), -l (list matching files only), " +
				"-c (count matches per file), -n (line numbers, on by default here). " +
				"The pattern is a regex by default. Returns matching lines as `path:line:text`. " +
				"Choose your own search terms from the task.",
			InputSchema: mustSchema(`{
				"type":"object",
				"properties":{
					"pattern":{"type":"string","description":"ripgrep regex (or fixed string with -F in args)"},
					"args":{"type":"array","items":{"type":"string"},"description":"additional ripgrep flags, e.g. [\"-t\",\"py\",\"-w\"]"}
				},
				"required":["pattern"]
			}`),
		},
		{
			Name: "read_file",
			Description: "Read a file from the repository. Provide `path` (repo-relative). " +
				"Optionally provide `start` and `end` (1-based, inclusive) to read only a line range; " +
				"omit both to read the whole file. Returns the requested lines with line numbers.",
			InputSchema: mustSchema(`{
				"type":"object",
				"properties":{
					"path":{"type":"string"},
					"start":{"type":"integer","description":"1-based first line (optional)"},
					"end":{"type":"integer","description":"1-based last line, inclusive (optional)"}
				},
				"required":["path"]
			}`),
		},
		{
			Name: "list_files",
			Description: "List files in the repository, optionally filtered by a glob (e.g. '**/*.py', 'src/**'). " +
				"Returns repo-relative paths. Honors typical ignore rules (.git, node_modules, etc.).",
			InputSchema: mustSchema(`{
				"type":"object",
				"properties":{"glob":{"type":"string","description":"optional glob filter"}}
			}`),
		},
	}
}

func (g *GrepArm) Execute(ctx context.Context, name string, input json.RawMessage) (string, bool) {
	switch name {
	case "grep":
		return g.grep(ctx, input)
	case "read_file":
		return g.readFile(input)
	case "list_files":
		return g.listFiles(ctx, input)
	default:
		return "unknown tool: " + name, true
	}
}

func (g *GrepArm) grep(ctx context.Context, input json.RawMessage) (string, bool) {
	var a struct {
		Pattern string   `json:"pattern"`
		Args    []string `json:"args"`
	}
	if err := json.Unmarshal(input, &a); err != nil {
		return "invalid grep args: " + err.Error(), true
	}
	if a.Pattern == "" {
		return "grep requires a non-empty pattern", true
	}
	// Default to line numbers + heading-less path:line:text. Reject flags that
	// would let the model escape the repo or change cwd semantics.
	args := []string{"--line-number", "--no-heading", "--color=never"}
	for _, f := range a.Args {
		if strings.HasPrefix(f, "--") && (strings.Contains(f, "pre") || strings.Contains(f, "hostname")) {
			continue // ignore exotic flags that shell out
		}
		args = append(args, f)
	}
	args = append(args, a.Pattern, ".")
	cmd := exec.CommandContext(ctx, g.RGPath, args...)
	cmd.Dir = g.Root
	out, err := cmd.CombinedOutput()
	// rg exits 1 when there are no matches — that's not an error for us.
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return "(no matches)", false
		}
		return fmt.Sprintf("grep error: %v\n%s", err, string(out)), true
	}
	s := string(out)
	if s == "" {
		return "(no matches)", false
	}
	return capString(s, g.MaxRead), false
}

func (g *GrepArm) readFile(input json.RawMessage) (string, bool) {
	var a struct {
		Path  string `json:"path"`
		Start int    `json:"start"`
		End   int    `json:"end"`
	}
	if err := json.Unmarshal(input, &a); err != nil {
		return "invalid read_file args: " + err.Error(), true
	}
	full, ok := g.safeJoin(a.Path)
	if !ok {
		return "path escapes repository root: " + a.Path, true
	}
	f, err := os.Open(full)
	if err != nil {
		return "cannot open " + a.Path + ": " + err.Error(), true
	}
	defer f.Close()

	var b strings.Builder
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	ln := 0
	for sc.Scan() {
		ln++
		if a.Start > 0 && ln < a.Start {
			continue
		}
		if a.End > 0 && ln > a.End {
			break
		}
		fmt.Fprintf(&b, "%d\t%s\n", ln, sc.Text())
		if b.Len() > g.MaxRead {
			b.WriteString("... (truncated)\n")
			break
		}
	}
	if b.Len() == 0 {
		return "(empty or range out of bounds)", false
	}
	return b.String(), false
}

func (g *GrepArm) listFiles(ctx context.Context, input json.RawMessage) (string, bool) {
	var a struct {
		Glob string `json:"glob"`
	}
	_ = json.Unmarshal(input, &a)
	// Use rg --files (honors ignore rules) with optional glob.
	args := []string{"--files"}
	if a.Glob != "" {
		args = append(args, "-g", a.Glob)
	}
	cmd := exec.CommandContext(ctx, g.RGPath, args...)
	cmd.Dir = g.Root
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return "(no files)", false
		}
		return fmt.Sprintf("list_files error: %v\n%s", err, string(out)), true
	}
	return capString(string(out), g.MaxRead), false
}

// safeJoin resolves a repo-relative path and confirms it stays under Root.
func (g *GrepArm) safeJoin(rel string) (string, bool) {
	clean := filepath.Clean(filepath.Join(g.Root, rel))
	rootAbs, _ := filepath.Abs(g.Root)
	cleanAbs, _ := filepath.Abs(clean)
	if cleanAbs != rootAbs && !strings.HasPrefix(cleanAbs, rootAbs+string(os.PathSeparator)) {
		return "", false
	}
	return clean, true
}

func capString(s string, max int) string {
	if max <= 0 {
		max = 200_000
	}
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... (output truncated at " + fmt.Sprint(max) + " bytes)"
}

func mustSchema(s string) json.RawMessage {
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		panic("bad schema: " + err.Error())
	}
	return json.RawMessage(s)
}
