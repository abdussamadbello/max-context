package ceiling

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Arm names, used in results and in the report table.
const (
	ArmGrepOneShot = "grep(one-shot)"
	ArmGrepChained = "grep(alias-chained)"
	ArmMaxContext  = "max-context"
)

// RunGrep executes the declared pattern battery with real ripgrep and
// attributes every hit to its enclosing function.
//
// Two tiers are run because reporting only the first would misrepresent the
// baseline:
//
//   - one-shot: the declared patterns for the symbol. This is what a text search
//     for the name yields.
//   - alias-chained: the same, plus a follow-up search for every alias found in
//     an import line in the first tier's output. This is what a skilled engineer
//     does next, and on an aliased symbol it is the tier that actually competes.
//
// The cost fields are what separate the tiers when both reach full recall.
func RunGrep(ctx context.Context, root string, p Probe, extra []string, rgPath string, chained bool) ArmResult {
	name := ArmGrepOneShot
	if chained {
		name = ArmGrepChained
	}
	res := ArmResult{Arm: name}

	patterns := append(append([]string(nil), p.GrepPatterns...), extra...)
	if len(patterns) == 0 {
		patterns = []string{regexpQuote(p.Symbol)}
	}

	fileCache := map[string][]string{}
	unattributable := map[string]bool{}
	var combined strings.Builder

	run := func(pattern, note string) {
		out, err := rg(ctx, rgPath, root, pattern)
		res.ToolCalls++
		res.OutputBytes += len(out)
		res.Steps = append(res.Steps, Step{Arm: name, Detail: "rg " + pattern, Bytes: len(out), Note: note})
		if err != nil {
			res.Err = err.Error()
			return
		}
		combined.WriteString(out)
		for _, h := range parseHits(out) {
			full := filepath.Join(root, h.Path)
			lines, ok := fileCache[full]
			if !ok {
				lines = readLines(full)
				fileCache[full] = lines
			}
			// A hit in a language this harness cannot attribute would silently
			// score as "no caller found", making a harness gap look like a grep
			// failure. Record it on the step instead.
			if _, known := defPatternFor(h.Path); !known {
				unattributable[filepath.Ext(h.Path)] = true
				continue
			}
			if fn := enclosingFunc(h.Path, lines, h.Line); fn != "" {
				res.Predicted = append(res.Predicted, fn)
			}
		}
	}

	for _, pattern := range patterns {
		run(pattern, "")
	}

	if chained {
		for _, alias := range discoverAliases(combined.String(), p.Symbol) {
			run(regexpQuote(alias)+`\s*\(`, "follow-up: `"+p.Symbol+"` is imported as `"+alias+"`")
		}
	}

	// Say so loudly when hits were dropped for lack of an attributor: a silent
	// drop scores as a grep miss and would understate the baseline.
	if len(unattributable) > 0 {
		exts := make([]string, 0, len(unattributable))
		for ext := range unattributable {
			exts = append(exts, ext)
		}
		sort.Strings(exts)
		res.Err = "harness cannot attribute hits in " + strings.Join(exts, ", ") +
			"; this arm's recall is a floor, not a measurement (add a pattern to defPatternFor)"
	}

	res.finalize(p.ExpectedSymbols)
	return res
}

// rg runs one ripgrep search confined to root. No matches is not an error.
func rg(ctx context.Context, rgPath, root, pattern string) (string, error) {
	if rgPath == "" {
		rgPath = "rg"
	}
	cmd := exec.CommandContext(ctx, rgPath, "--line-number", "--no-heading", "--color=never", pattern, ".")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return "", nil // no matches
		}
		return "", fmt.Errorf("rg %q: %v: %s", pattern, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// parseHits turns ripgrep's `./path:line:text` rows into structured hits.
func parseHits(out string) []hit {
	var hits []hit
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		// Split on the first two colons only; the text itself may contain colons.
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		j := strings.Index(line[i+1:], ":")
		if j < 0 {
			continue
		}
		n, err := strconv.Atoi(line[i+1 : i+1+j])
		if err != nil {
			continue
		}
		hits = append(hits, hit{
			Path: strings.TrimPrefix(line[:i], "./"),
			Line: n,
			Text: line[i+j+2:],
		})
	}
	return hits
}

// regexpQuote escapes a literal symbol name for use as a ripgrep pattern.
func regexpQuote(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`.+*?()|[]{}^$\`, r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
