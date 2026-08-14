package ceiling

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// defRe matches a Python function definition, capturing its indentation and name.
var defRe = regexp.MustCompile(`^(\s*)(?:async\s+)?def\s+(\w+)`)

// enclosingFunc returns the name of the function containing the given 1-based
// line, or "" if the line sits at module level.
//
// This does for grep, mechanically and for free, the step a human or model has
// to do by hand: turn a `path:line` hit into the name of the calling function.
// Handing the baseline that work is deliberate — the point of the comparison is
// whether the call site is REACHABLE at all, not whether attribution is tedious.
//
// The walk is indentation-based so nested defs attribute to the innermost
// enclosing function, and a hit ON a `def` line (the definition itself, not a
// call) attributes to whatever encloses it — module level for a top-level def,
// which correctly scores as "no caller found".
func enclosingFunc(lines []string, line int) string {
	if line < 1 || line > len(lines) {
		return ""
	}
	target := lines[line-1]
	indent := leadingWidth(target)
	// A hit on the def line itself belongs to the def's parent scope, so treat
	// its indentation as the def's own.
	for i := line - 2; i >= 0; i-- {
		l := lines[i]
		if strings.TrimSpace(l) == "" {
			continue
		}
		m := defRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if leadingWidth(m[1]) < indent {
			return m[2]
		}
	}
	return ""
}

// leadingWidth counts leading whitespace, expanding tabs to the next multiple
// of 8 so mixed indentation still orders correctly.
func leadingWidth(s string) int {
	w := 0
	for _, r := range s {
		switch r {
		case ' ':
			w++
		case '\t':
			w += 8 - (w % 8)
		default:
			return w
		}
	}
	return w
}

// readLines loads a file as a line slice, or nil if unreadable.
func readLines(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

// aliasImportRe matches `from X import SYM as ALIAS`, capturing SYM and ALIAS.
// Rebinding an import under another name is the whole mechanism this benchmark
// probes: a text search for SYM finds this line and then stops, because every
// real call site reads ALIAS.
var aliasImportRe = regexp.MustCompile(`\bimport\s+(?:.*?,\s*)?(\w+)\s+as\s+(\w+)`)

// discoverAliases scans raw grep output for import lines that rebind symbol
// under a different name, returning the aliases. This mechanizes the follow-up
// a skilled engineer performs on seeing `import post_transaction as apply_entry`
// in their first result — without it the baseline is a strawman.
func discoverAliases(grepOutput, symbol string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(grepOutput, "\n") {
		for _, m := range aliasImportRe.FindAllStringSubmatch(line, -1) {
			if m[1] != symbol || m[2] == symbol || seen[m[2]] {
				continue
			}
			seen[m[2]] = true
			out = append(out, m[2])
		}
	}
	return out
}

// hit is one `path:line:text` row from ripgrep.
type hit struct {
	Path string
	Line int
	Text string
}
