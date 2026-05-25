package artifacts

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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

	// Directory map (top-level dirs with file counts)
	b.WriteString("\n## Directory map\n\n")
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
				parts := strings.Split(p, "/")
				if len(parts) > 0 {
					top := parts[0]
					dirCounts[top] += cnt
				}
			}
		}
		dirRows.Close()
		for d, n := range dirCounts {
			b.WriteString(fmt.Sprintf("- %s/: %d symbols\n", d, n))
		}
	}

	// High-connectivity functions (most called)
	b.WriteString("\n## High-connectivity functions\n\n")
	callRows, err := database.Query(`
		SELECT f.name, f.file_path, COUNT(*) as call_count
		FROM calls c JOIN functions f ON f.id = c.callee_id
		GROUP BY c.callee_id ORDER BY call_count DESC LIMIT 10
	`)
	if err == nil {
		for callRows.Next() {
			var name, path string
			var n int
			if callRows.Scan(&name, &path, &n) == nil {
				b.WriteString(fmt.Sprintf("- %s (%s): %d callers\n", name, path, n))
			}
		}
		callRows.Close()
	}

	// Module dependencies (imports)
	b.WriteString("\n## Module dependencies\n\n")
	impRows, err := database.Query(`
		SELECT file_path, imported_path FROM imports LIMIT 30
	`)
	if err == nil {
		for impRows.Next() {
			var from, to string
			if impRows.Scan(&from, &to) == nil {
				b.WriteString(fmt.Sprintf("- %s -> %s\n", from, to))
			}
		}
		impRows.Close()
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
