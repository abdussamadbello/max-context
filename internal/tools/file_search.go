package tools

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// searchFiles searches the indexed file inventory rather than symbol contents.
// Every query term must appear in the path; exact and basename matches rank
// first, followed by shorter paths and a deterministic lexical tie-break.
func searchFiles(database *sql.DB, query string, fileFilter *regexp.Regexp, limit int) ([]searchResult, error) {
	rows, err := database.Query(`
		SELECT file_path FROM file_summaries
		UNION SELECT file_path FROM functions
		UNION SELECT file_path FROM types
		UNION SELECT file_path FROM documents
		UNION SELECT file_path FROM imports`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	terms := splitTerms(strings.ToLower(query))
	var paths []string
	for rows.Next() {
		var file string
		if err := rows.Scan(&file); err != nil {
			return nil, err
		}
		lower := strings.ToLower(filepath.ToSlash(file))
		matched := len(terms) > 0
		for _, term := range terms {
			if !strings.Contains(lower, strings.ToLower(term)) {
				matched = false
				break
			}
		}
		if matched && pathMatchesCompiled(file, fileFilter) {
			paths = append(paths, filepath.ToSlash(file))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	q := strings.ToLower(filepath.ToSlash(strings.TrimSpace(query)))
	sort.Slice(paths, func(i, j int) bool {
		a, b := strings.ToLower(paths[i]), strings.ToLower(paths[j])
		aBase, bBase := strings.ToLower(filepath.Base(a)), strings.ToLower(filepath.Base(b))
		aExact, bExact := a == q || aBase == q, b == q || bBase == q
		if aExact != bExact {
			return aExact
		}
		if len(a) != len(b) {
			return len(a) < len(b)
		}
		return a < b
	})
	if len(paths) > limit {
		paths = paths[:limit]
	}
	out := make([]searchResult, 0, len(paths))
	for _, file := range paths {
		out = append(out, searchResult{File: file, Kind: "file", Name: filepath.Base(file)})
	}
	return out, nil
}

func nearbyFiles(database *sql.DB, query string, fileFilter *regexp.Regexp) []string {
	terms := splitTerms(strings.ToLower(query))
	if len(terms) == 0 {
		return nil
	}
	rows, err := database.Query(`
		SELECT file_path FROM file_summaries
		UNION SELECT file_path FROM functions
		UNION SELECT file_path FROM types
		UNION SELECT file_path FROM documents
		UNION SELECT file_path FROM imports`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var file string
		if rows.Scan(&file) != nil || !pathMatchesCompiled(file, fileFilter) {
			continue
		}
		lower := strings.ToLower(file)
		for _, term := range terms {
			prefixLen := len(term)
			if prefixLen > 4 {
				prefixLen = 4
			}
			if prefixLen >= 3 && strings.Contains(lower, term[:prefixLen]) {
				out = append(out, filepath.ToSlash(file))
				break
			}
		}
		if len(out) == 5 {
			break
		}
	}
	sort.Strings(out)
	return out
}

func pathMatchesFilter(file, pattern string) bool {
	if pattern == "" {
		return true
	}
	re, err := compilePathGlob(pattern)
	return err == nil && pathMatchesCompiled(file, re)
}

func pathMatchesCompiled(file string, pattern *regexp.Regexp) bool {
	return pattern == nil || pattern.MatchString(filepath.ToSlash(file))
}

// compilePathGlob implements the documented slash-separated glob syntax,
// including ** across directories. A pattern without a slash matches basenames.
func compilePathGlob(pattern string) (*regexp.Regexp, error) {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" {
		return regexp.Compile(`^.*$`)
	}
	var b strings.Builder
	b.WriteByte('^')
	if !strings.Contains(pattern, "/") {
		b.WriteString(`(?:.*/)?`)
	}
	for i := 0; i < len(pattern); {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i += 2
				if i < len(pattern) && pattern[i] == '/' {
					b.WriteString(`(?:.*/)?`)
					i++
				} else {
					b.WriteString(`.*`)
				}
			} else {
				b.WriteString(`[^/]*`)
				i++
			}
		case '?':
			b.WriteString(`[^/]`)
			i++
		case '[':
			end := i + 1
			for end < len(pattern) && pattern[end] != ']' {
				end++
			}
			if end == len(pattern) || end == i+1 {
				return nil, fmt.Errorf("unterminated character class")
			}
			class := pattern[i+1 : end]
			b.WriteByte('[')
			if class[0] == '!' {
				b.WriteByte('^')
				class = class[1:]
			}
			if class == "" {
				return nil, fmt.Errorf("empty character class")
			}
			for j := 0; j < len(class); j++ {
				if class[j] == '\\' || class[j] == ']' {
					b.WriteByte('\\')
				}
				b.WriteByte(class[j])
			}
			b.WriteByte(']')
			i = end + 1
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
			i++
		}
	}
	b.WriteByte('$')
	return regexp.Compile(b.String())
}
