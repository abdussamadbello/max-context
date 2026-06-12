package indexer

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/maxcontext/max-context/pkg/treesitter"
)

// DefaultIgnoreDirNames are directory names always skipped when scanning.
var DefaultIgnoreDirNames = map[string]bool{
	"node_modules": true, ".git": true, "dist": true, "build": true,
	"vendor": true, "__pycache__": true, ".max-context": true,
	".venv": true, "venv": true, ".env": true, "env": true,
	".tox": true, ".mypy_cache": true, ".pytest_cache": true,
	".next": true, ".nuxt": true, ".output": true, "coverage": true,
	"target": true, ".cargo": true,
}

// Scanner discovers indexable source files under project root.
type Scanner struct {
	Root       string
	Ignore     *IgnoreMatcher
	Extensions []string
	// Includes, when non-empty, restricts files to those matching at least one
	// glob (matched against the slash-separated relative path, or the basename
	// for patterns without a path separator). Note filepath.Match has no `**`.
	Includes []string
	// MaxFileSize skips files larger than this many bytes (0 = unlimited).
	MaxFileSize int64
}

// IgnoreMatcher matches paths against gitignore-style rules.
type IgnoreMatcher struct {
	patterns []string
}

// NewIgnoreMatcher reads .gitignore files (root + subdirectories) and
// .max-context/ignore, building a combined matcher.
func NewIgnoreMatcher(root string) (*IgnoreMatcher, error) {
	var patterns []string

	// Read root-level ignore files
	for _, name := range []string{".gitignore", ".contextignore", filepath.Join(".max-context", "ignore")} {
		patterns = append(patterns, readIgnoreFile(filepath.Join(root, name), "")...)
	}

	// Walk one level of subdirectories for their .gitignore files
	entries, err := os.ReadDir(root)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() || DefaultIgnoreDirNames[e.Name()] {
				continue
			}
			subGitignore := filepath.Join(root, e.Name(), ".gitignore")
			patterns = append(patterns, readIgnoreFile(subGitignore, e.Name())...)
		}
	}

	return &IgnoreMatcher{patterns: patterns}, nil
}

// NewIgnoreMatcherWithExtra builds the standard ignore matcher plus extra
// exclude patterns (from .max-context/config.json "exclude"). Extra patterns
// use the same gitignore-style matching; `!` negations are ignored, as in
// Match.
func NewIgnoreMatcherWithExtra(root string, extra []string) (*IgnoreMatcher, error) {
	m, err := NewIgnoreMatcher(root)
	if err != nil {
		return nil, err
	}
	m.patterns = append(m.patterns, extra...)
	return m, nil
}

// matchesAnyGlob reports whether relPath matches at least one include glob.
// Globs match the full slash-separated relative path; patterns without a
// path separator also match the basename (so "*.go" matches nested files).
func matchesAnyGlob(globs []string, relPath string) bool {
	norm := filepath.ToSlash(relPath)
	base := filepath.Base(norm)
	for _, g := range globs {
		g = filepath.ToSlash(strings.TrimSpace(g))
		if g == "" {
			continue
		}
		if matched, _ := filepath.Match(g, norm); matched {
			return true
		}
		if !strings.Contains(g, "/") {
			if matched, _ := filepath.Match(g, base); matched {
				return true
			}
		}
	}
	return false
}

// readIgnoreFile reads a gitignore-style file and returns patterns.
// If prefix is non-empty, it's prepended to each pattern (for subdirectory gitignores).
func readIgnoreFile(path, prefix string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var patterns []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if prefix != "" {
			// Prefix subdirectory patterns so they match relative to root
			line = prefix + "/" + strings.TrimPrefix(line, "/")
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// isGlob returns true if the pattern contains glob metacharacters.
func isGlob(p string) bool {
	return strings.ContainsAny(p, "*?[")
}

// Match returns true if path (relative to root) should be ignored.
func (m *IgnoreMatcher) Match(relPath string) bool {
	norm := filepath.ToSlash(relPath)
	for _, p := range m.patterns {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" || strings.HasPrefix(p, "!") {
			continue
		}
		if strings.HasPrefix(p, "/") {
			p = p[1:]
		}

		isDir := strings.HasSuffix(p, "/")
		if isDir {
			p = strings.TrimSuffix(p, "/")
		}

		if isGlob(p) {
			// Glob pattern: try matching against full path and each basename segment
			if matched, _ := filepath.Match(p, norm); matched {
				return true
			}
			// Also try matching just the pattern's base against each path segment
			// e.g. pattern "*.pyc" should match "foo/bar/test.pyc"
			base := filepath.Base(norm)
			if !strings.Contains(p, "/") {
				if matched, _ := filepath.Match(p, base); matched {
					return true
				}
			}
		} else {
			// Simple literal matching
			if isDir {
				if norm == p || strings.HasPrefix(norm, p+"/") || strings.Contains(norm, "/"+p+"/") {
					return true
				}
			} else {
				if norm == p || strings.HasSuffix(norm, "/"+p) || strings.Contains(norm, "/"+p+"/") {
					return true
				}
			}
		}
	}
	return false
}

// Scan walks the project and returns relative paths of indexable files.
func (s *Scanner) Scan() ([]string, error) {
	extSet := make(map[string]bool)
	for _, e := range s.Extensions {
		extSet[e] = true
	}
	if len(extSet) == 0 {
		for _, e := range treesitter.SupportedExtensions() {
			extSet[e] = true
		}
	}

	var out []string
	err := filepath.Walk(s.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsPermission(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if DefaultIgnoreDirNames[base] {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(s.Root, path)
			if rel == "." {
				return nil
			}
			if s.Ignore != nil && s.Ignore.Match(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(s.Root, path)
		if s.Ignore != nil && s.Ignore.Match(rel) {
			return nil
		}
		if s.MaxFileSize > 0 && info.Size() > s.MaxFileSize {
			return nil
		}
		if len(s.Includes) > 0 && !matchesAnyGlob(s.Includes, rel) {
			return nil
		}
		ext := filepath.Ext(path)
		if extSet[ext] {
			if _, ok := treesitter.LanguageForExt(ext); ok {
				out = append(out, rel)
			}
		}
		return nil
	})
	return out, err
}
