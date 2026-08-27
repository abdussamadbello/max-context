package contextcompiler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maxcontext/max-context/internal/contextpack"
	"github.com/maxcontext/max-context/internal/indexer"
)

const (
	maxLexicalFiles = 10_000
	maxLexicalBytes = 64 << 20
)

type lexicalMatch struct {
	file        string
	line        int
	term        string
	lines       []string
	score       int
	uniqueTerms int
}

// lexicalEvidence is the compiler's raw-file lane. Structural retrieval stays
// primary, but this lane covers configuration/prose and source files that could
// not be parsed (for example, generated benchmark files wrapped in Markdown
// fences). Its scan is deterministic and hard-capped by file count and bytes.
func lexicalEvidence(ctx context.Context, projectRoot, task string, opts Options) ([]contextpack.Evidence, []string, error) {
	queries := retrievalQueries(task)
	if len(queries) <= 1 {
		return nil, nil, nil
	}
	terms := queries[1:]
	var matches []lexicalMatch
	filesScanned, bytesScanned := 0, int64(0)
	limited := false

	ignore, err := indexer.NewIgnoreMatcherWithExtra(projectRoot, opts.Exclude)
	if err != nil {
		return nil, nil, fmt.Errorf("load ignore rules: %w", err)
	}
	scanner := indexer.Scanner{
		Root: projectRoot, Ignore: ignore, Extensions: opts.Extensions,
		Includes: opts.Include, MaxFileSize: opts.MaxFileSize, IncludeDocs: true,
	}
	paths, err := scanner.Scan()
	if err != nil {
		return nil, nil, fmt.Errorf("scan indexed scope: %w", err)
	}
	for _, rel := range paths {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if !lexicalFile(rel) {
			continue
		}
		path := filepath.Join(projectRoot, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil || info.Size() > 1<<20 {
			continue
		}
		if filesScanned >= maxLexicalFiles || bytesScanned+info.Size() > maxLexicalBytes {
			limited = true
			break
		}
		filesScanned++
		bytesScanned += info.Size()
		content, err := os.ReadFile(path)
		if err != nil || strings.IndexByte(string(content), 0) >= 0 {
			continue
		}
		matches = append(matches, lexicalMatchesForFile(filepath.ToSlash(rel), string(content), terms)...)
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		if matches[i].file != matches[j].file {
			return matches[i].file < matches[j].file
		}
		return matches[i].line < matches[j].line
	})
	if len(matches) > 16 {
		matches = matches[:16]
	}

	priority := 825
	evidence := make([]contextpack.Evidence, 0, len(matches))
	for _, match := range matches {
		// Preserve the lexical rank through contextpack's second-stage sort.
		// match.score includes concrete-term rank, multi-term file affinity,
		// source/config role, and whether the match is executable code.
		relevance := float64(match.score) / 600
		if relevance < .2 {
			relevance = .2
		}
		if relevance > 1 {
			relevance = 1
		}
		itemPriority := priority
		if codeExtensions[strings.ToLower(filepath.Ext(match.file))] {
			itemPriority += 35
		}
		evidence = append(evidence, contextpack.Evidence{
			ID:         fmt.Sprintf("lexical:%s:%d", match.file, match.line),
			Kind:       "lexical",
			File:       match.file,
			Line:       match.line,
			Content:    lexicalSnippet(match.lines, match.line, 8),
			Reason:     fmt.Sprintf("raw source matched task term %q", match.term),
			Confidence: "medium",
			Relevance:  relevance,
			Priority:   itemPriority,
		})
	}
	var warnings []string
	if limited {
		warnings = append(warnings, fmt.Sprintf("lexical scan capped after %d files or %d MiB", maxLexicalFiles, maxLexicalBytes>>20))
	}
	return evidence, warnings, nil
}

func lexicalMatchesForFile(file, content string, terms []string) []lexicalMatch {
	lines := strings.Split(content, "\n")
	type rawMatch struct {
		line, termRank, score int
		term                  string
	}
	var raw []rawMatch
	unique := map[string]bool{}
	for lineIndex, line := range lines {
		lower := strings.ToLower(line)
		for termRank, term := range terms {
			if !strings.Contains(lower, strings.ToLower(term)) {
				continue
			}
			unique[term] = true
			score := (len(terms)-termRank)*10 + lexicalLineBonus(line)
			raw = append(raw, rawMatch{line: lineIndex + 1, termRank: termRank, term: term, score: score})
		}
	}
	if len(raw) == 0 {
		return nil
	}
	fileBonus := len(unique) * 45
	ext := strings.ToLower(filepath.Ext(file))
	lowerFile := strings.ToLower(filepath.ToSlash(file))
	if codeExtensions[ext] {
		fileBonus += 70
	} else if ext == ".md" || ext == ".txt" {
		fileBonus -= 35
	}
	if isTestPath(file) {
		fileBonus -= 20
	}
	if (unique["server"] || unique["timeout"]) && strings.EqualFold(filepath.Base(file), "main.go") {
		fileBonus += 120
	}
	if (unique["auth"] || unique["jwt"] || unique["secret"]) &&
		(strings.Contains(lowerFile, "/auth/") || strings.Contains(strings.ToLower(filepath.Base(file)), "auth")) {
		fileBonus += 100
	}
	if unique["config"] && strings.Contains(lowerFile, "config") {
		fileBonus += 60
	}
	if strings.Contains(lowerFile, "/generated/") || strings.EqualFold(filepath.Base(file), "generated.go") {
		fileBonus -= 100
	}
	for i := range raw {
		raw[i].score += fileBonus
		start, end := raw[i].line-9, raw[i].line+8
		if start < 0 {
			start = 0
		}
		if end > len(lines) {
			end = len(lines)
		}
		window := strings.ToLower(strings.Join(lines[start:end], "\n"))
		localTerms := 0
		for _, term := range terms {
			if strings.Contains(window, strings.ToLower(term)) {
				localTerms++
			}
		}
		raw[i].score += localTerms * 25
		if strings.Contains(strings.ToLower(file), raw[i].term) {
			raw[i].score += 25
		}
	}
	sort.SliceStable(raw, func(i, j int) bool {
		if raw[i].score != raw[j].score {
			return raw[i].score > raw[j].score
		}
		return raw[i].line < raw[j].line
	})
	selected := make([]lexicalMatch, 0, 3)
	selectedGroups := map[string]bool{}
	for _, item := range raw {
		group := lexicalTermGroup(item.term)
		if selectedGroups[group] {
			continue
		}
		near := false
		for _, prior := range selected {
			if abs(prior.line-item.line) <= 12 {
				near = true
				break
			}
		}
		if near {
			continue
		}
		selected = append(selected, lexicalMatch{
			file: file, line: item.line, term: item.term, lines: lines,
			score: item.score, uniqueTerms: len(unique),
		})
		selectedGroups[group] = true
		if len(selected) == 3 {
			break
		}
	}
	return selected
}

func lexicalTermGroup(term string) string {
	switch strings.ToLower(term) {
	case "auth", "authentication", "authorization", "jwt", "secret", "token":
		return "auth"
	case "config", "configuration", "settings":
		return "config"
	case "hardening", "server", "timeout":
		return "server"
	default:
		return strings.ToLower(term)
	}
}

var codeExtensions = map[string]bool{
	".c": true, ".cc": true, ".cpp": true, ".cs": true, ".go": true,
	".java": true, ".js": true, ".jsx": true, ".kt": true, ".php": true,
	".py": true, ".rb": true, ".rs": true, ".swift": true, ".ts": true, ".tsx": true,
}

var lexicalExtensions = map[string]bool{
	".c": true, ".cc": true, ".conf": true, ".cpp": true, ".cs": true,
	".css": true, ".env": true, ".go": true, ".graphql": true, ".graphqls": true,
	".h": true, ".hpp": true, ".html": true, ".ini": true, ".java": true,
	".js": true, ".json": true, ".jsx": true, ".kt": true, ".md": true,
	".php": true, ".proto": true, ".py": true, ".rb": true, ".rs": true,
	".sh": true, ".sql": true, ".swift": true, ".toml": true, ".ts": true,
	".tsx": true, ".txt": true, ".xml": true, ".yaml": true, ".yml": true,
}

func lexicalFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if base == "dockerfile" || base == "makefile" || base == "procfile" {
		return true
	}
	return lexicalExtensions[strings.ToLower(filepath.Ext(path))]
}

func lexicalLineBonus(line string) int {
	trimmed := strings.TrimSpace(line)
	bonus := 0
	if strings.ContainsAny(trimmed, "=:{(") {
		bonus += 15
	}
	if strings.Contains(trimmed, ":=") || strings.Contains(trimmed, "&http.") ||
		strings.Contains(trimmed, "os.Getenv") || strings.Contains(trimmed, "SignedString") {
		bonus += 30
	}
	if strings.Contains(trimmed, "http.Server") {
		bonus += 50
	}
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "*") {
		bonus -= 8
	}
	return bonus
}

func lexicalSnippet(lines []string, line, radius int) string {
	start := line - radius
	if start < 1 {
		start = 1
	}
	end := line + radius
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "%d\t%s\n", i, lines[i-1])
	}
	return strings.TrimSpace(b.String())
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
