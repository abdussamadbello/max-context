package indexer

import (
	"path/filepath"
	"strings"
)

// DocExtensions maps non-code file extensions to a document kind. These files
// are indexed as plain text (documents table + documents_fts) so
// query_codebase can find schemas, configs, and docs — no tree-sitter parsing
// and no call-graph involvement.
var DocExtensions = map[string]string{
	".md":        "markdown",
	".markdown":  "markdown",
	".rst":       "text",
	".txt":       "text",
	".yaml":      "yaml",
	".yml":       "yaml",
	".json":      "json",
	".toml":      "toml",
	".proto":     "proto",
	".graphql":   "graphql",
	".graphqls":  "graphql",
	".sql":       "sql",
	".xml":       "xml",
	".dockerfile": "dockerfile",
}

// docSkipBasenames are machine-generated files that match a doc extension but
// are large, noisy, and useless as search results.
var docSkipBasenames = map[string]bool{
	"package-lock.json":  true,
	"yarn.lock":          true,
	"pnpm-lock.yaml":     true,
	"npm-shrinkwrap.json": true,
	"Cargo.lock":         true,
	"poetry.lock":        true,
	"Pipfile.lock":       true,
	"composer.lock":      true,
	"Gemfile.lock":       true,
	"go.sum":             true,
	"flake.lock":         true,
}

// DocKindForPath returns the document kind for a non-code file path and true,
// or ("", false) when the file should not be indexed as a document.
func DocKindForPath(relPath string) (string, bool) {
	base := filepath.Base(filepath.ToSlash(relPath))
	if docSkipBasenames[base] || strings.Contains(base, ".min.") {
		return "", false
	}
	if base == "Dockerfile" || strings.HasPrefix(base, "Dockerfile.") {
		return "dockerfile", true
	}
	kind, ok := DocExtensions[strings.ToLower(filepath.Ext(base))]
	return kind, ok
}

// docTitle returns a display title for a document: the first markdown heading
// when present, the basename otherwise.
func docTitle(relPath string, content []byte) string {
	if strings.HasSuffix(strings.ToLower(relPath), ".md") || strings.HasSuffix(strings.ToLower(relPath), ".markdown") {
		for _, line := range strings.Split(string(content), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#") {
				return strings.TrimSpace(strings.TrimLeft(line, "#"))
			}
		}
	}
	return filepath.Base(filepath.ToSlash(relPath))
}
