package artifacts

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// topLevelDir returns the first path segment ("internal/db/x.go" -> "internal").
func topLevelDir(p string) string {
	if i := strings.IndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return p
}

// topFilesByScore returns the n highest-scoring files, deterministically.
func topFilesByScore(scores map[string]float64, n int) []string {
	files := make([]string, 0, len(scores))
	for f := range scores {
		files = append(files, f)
	}
	sort.Slice(files, func(a, b int) bool {
		if scores[files[a]] != scores[files[b]] {
			return scores[files[a]] > scores[files[b]]
		}
		return files[a] < files[b]
	})
	if len(files) > n {
		files = files[:n]
	}
	return files
}

// WriteArchitecture generates .max-context/architecture.md from the database.
func WriteArchitecture(dir string, database *sql.DB) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	var b strings.Builder

	// Tech stack: language counts
	b.WriteString("# Architecture\n\n## Tech stack\n\n")
	rows, err := database.Query(`
		SELECT language, COUNT(*) as cnt FROM functions GROUP BY language ORDER BY cnt DESC
	`)
	if err == nil {
		for rows.Next() {
			var lang string
			var cnt int
			if rows.Scan(&lang, &cnt) == nil {
				b.WriteString(fmt.Sprintf("- %s: %d functions\n", lang, cnt))
			}
		}
		rows.Close()
	}

	// PageRank over the call graph drives the ranked sections below: which
	// functions the codebase structurally depends on, which directories carry
	// that weight, and which files' imports matter most. Also makes the output
	// deterministic (the old map iteration produced random section order).
	ranked, rankErr := PageRankFunctions(database)
	fileScores := FileScores(ranked)

	// Directory map: top-level dirs with symbol counts, ordered by aggregate
	// PageRank weight (structural importance), not name or map order.
	b.WriteString("\n## Directory map\n\n")
	type dirEntry struct {
		name    string
		symbols int
		score   float64
	}
	dirRows, err := database.Query(`
		SELECT file_path, COUNT(*) as cnt FROM functions
		GROUP BY file_path
	`)
	if err == nil {
		dirCounts := make(map[string]int)
		for dirRows.Next() {
			var p string
			var cnt int
			if dirRows.Scan(&p, &cnt) == nil {
				dirCounts[topLevelDir(p)] += cnt
			}
		}
		dirRows.Close()
		dirScores := make(map[string]float64)
		for f, s := range fileScores {
			dirScores[topLevelDir(f)] += s
		}
		var dirs []dirEntry
		for d, n := range dirCounts {
			dirs = append(dirs, dirEntry{name: d, symbols: n, score: dirScores[d]})
		}
		sort.Slice(dirs, func(a, b int) bool {
			if dirs[a].score != dirs[b].score {
				return dirs[a].score > dirs[b].score
			}
			return dirs[a].name < dirs[b].name
		})
		for _, d := range dirs {
			b.WriteString(fmt.Sprintf("- %s/: %d symbols\n", d.name, d.symbols))
		}
	}

	// Key functions: top PageRank — the symbols the call graph converges on.
	b.WriteString("\n## Key functions (PageRank)\n\n")
	if rankErr == nil {
		for i, f := range ranked {
			if i >= 15 {
				break
			}
			b.WriteString(fmt.Sprintf("- %s (%s)\n", f.Name, f.FilePath))
		}
	}

	// Module dependencies: imports of the highest-ranked files (instead of an
	// arbitrary LIMIT 30 over the whole imports table).
	b.WriteString("\n## Module dependencies\n\n")
	depLines := 0
	for _, file := range topFilesByScore(fileScores, 10) {
		impRows, err := database.Query(
			`SELECT DISTINCT imported_path FROM imports WHERE file_path = ? ORDER BY imported_path LIMIT 5`, file)
		if err != nil {
			continue
		}
		for impRows.Next() && depLines < 30 {
			var to string
			if impRows.Scan(&to) == nil {
				b.WriteString(fmt.Sprintf("- %s -> %s\n", file, to))
				depLines++
			}
		}
		impRows.Close()
		if depLines >= 30 {
			break
		}
	}

	p := filepath.Join(dir, "architecture.md")
	return os.WriteFile(p, []byte(b.String()), 0644)
}

// ReadArchitecture returns the contents of .max-context/architecture.md.
func ReadArchitecture(dir string) (string, error) {
	p := filepath.Join(dir, "architecture.md")
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
