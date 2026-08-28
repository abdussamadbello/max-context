package ceiling

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// defRe matches a Python function definition, capturing its indentation and name.
var defRe = regexp.MustCompile(`^(\s*)(?:async\s+)?def\s+(\w+)`)

// goFuncRe matches a Go function or method definition, capturing its
// indentation and name. The optional parenthesised group is the receiver, so
// `func (e *EmailNotifier) Send(...)` captures Send rather than the receiver.
var goFuncRe = regexp.MustCompile(`^(\s*)func\s+(?:\([^)]*\)\s*)?(\w+)`)

// tsFuncRe matches a TypeScript/JavaScript function or class method: an
// optional `export`, optional modifiers, then either `function name(` or a bare
// `name(` method. Interface members declare rather than define, so a body brace
// is required to keep `send(msg: string): void;` from counting as a definition.
var tsFuncRe = regexp.MustCompile(`^(\s*)(?:export\s+)?(?:default\s+)?(?:async\s+)?(?:public\s+|private\s+|protected\s+|static\s+)*(?:function\s+)?(\w+)\s*\([^)]*\)[^;{]*\{`)

// controlFlowKeywords look exactly like a call-shaped definition to a regex:
// `for (const n of ns) {` matches "identifier, parens, brace" as surely as
// `send(msg: string) {` does. Attributing a call to `for` silently costs the
// baseline every hit inside a loop — grep found the line, and the harness threw
// the answer away. RE2 has no lookahead, so the match is rejected here instead.
var controlFlowKeywords = map[string]bool{
	"for": true, "if": true, "while": true, "switch": true, "catch": true,
	"return": true, "else": true, "do": true, "with": true, "await": true,
	"typeof": true, "throw": true, "case": true,
}

// defPatternFor picks the definition pattern for a file's language. Attribution
// that silently matches nothing scores the baseline at zero on every hit it
// found, which reads as a retrieval failure and is really a harness failure —
// the strawman this harness exists not to be. An unknown extension therefore
// returns false, and the caller reports it rather than scoring it as a miss.
func defPatternFor(path string) (*regexp.Regexp, bool) {
	switch {
	case strings.HasSuffix(path, ".py"):
		return defRe, true
	case strings.HasSuffix(path, ".go"):
		return goFuncRe, true
	case strings.HasSuffix(path, ".ts"), strings.HasSuffix(path, ".tsx"),
		strings.HasSuffix(path, ".js"), strings.HasSuffix(path, ".jsx"):
		return tsFuncRe, true
	default:
		return nil, false
	}
}

// enclosingFunc returns the name of the function containing the given 1-based
// line, or "" if the line sits at module level.
//
// This does for grep, mechanically and for free, the step a human or model has
// to do by hand: turn a `path:line` hit into the name of the calling function.
// Handing the baseline that work is deliberate — the point of the comparison is
// whether the call site is REACHABLE at all, not whether attribution is tedious.
//
// The walk is indentation-based so nested defs attribute to the innermost
// enclosing function, and a hit ON a definition line (the definition itself,
// not a call) attributes to whatever encloses it — file level for a top-level
// definition, which correctly scores as "no caller found".
func enclosingFunc(path string, lines []string, line int) string {
	re, ok := defPatternFor(path)
	if !ok {
		return ""
	}
	if line < 1 || line > len(lines) {
		return ""
	}
	target := lines[line-1]
	indent := leadingWidth(target)
	// A hit on the definition line itself belongs to its parent scope, so treat
	// its indentation as the definition's own.
	for i := line - 2; i >= 0; i-- {
		l := lines[i]
		if strings.TrimSpace(l) == "" {
			continue
		}
		m := re.FindStringSubmatch(l)
		if m == nil || controlFlowKeywords[m[2]] {
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
