package artifacts

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
	"time"
)

const summaryTpl = `# Project Summary
**Indexed:** {{.LastIndex}}
**Functions:** {{.TotalFunctions}}
**Files:** {{.TotalFiles}}
## Structure
{{.DirSummary}}
## Entry points
{{.EntryPoints}}
`

type SummaryData struct {
	LastIndex      string
	TotalFunctions int
	TotalFiles     int
	DirSummary     string
	EntryPoints    string
}

func WriteSummary(dir string, database *sql.DB) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	var totalFuncs, totalFiles int
	if err := database.QueryRow("SELECT COUNT(*) FROM functions").Scan(&totalFuncs); err != nil {
		return fmt.Errorf("count functions: %w", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM (
		SELECT file_path FROM file_summaries
		UNION SELECT file_path FROM functions
		UNION SELECT file_path FROM types
		UNION SELECT file_path FROM imports
		UNION SELECT file_path FROM documents
	)`).Scan(&totalFiles); err != nil {
		return fmt.Errorf("count files: %w", err)
	}
	rows, err := database.Query("SELECT file_path FROM functions GROUP BY file_path ORDER BY file_path LIMIT 20")
	if err != nil {
		return fmt.Errorf("list structure files: %w", err)
	}
	var dirs []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return fmt.Errorf("scan structure file: %w", err)
		}
		dirs = append(dirs, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("list structure files: %w", err)
	}
	rows.Close()
	dirSummary := "See architecture.md."
	if len(dirs) > 0 {
		var b bytes.Buffer
		for _, d := range dirs {
			b.WriteString("- ")
			b.WriteString(d)
			b.WriteString("\n")
		}
		dirSummary = b.String()
	}
	entryRows, err := database.Query("SELECT DISTINCT file_path FROM functions WHERE file_path LIKE '%main.%' OR file_path LIKE '%index.%' LIMIT 10")
	if err != nil {
		return fmt.Errorf("list entry points: %w", err)
	}
	var entries []string
	for entryRows.Next() {
		var p string
		if err := entryRows.Scan(&p); err != nil {
			entryRows.Close()
			return fmt.Errorf("scan entry point: %w", err)
		}
		entries = append(entries, p)
	}
	if err := entryRows.Err(); err != nil {
		entryRows.Close()
		return fmt.Errorf("list entry points: %w", err)
	}
	entryRows.Close()
	entryPoints := "None."
	if len(entries) > 0 {
		var b bytes.Buffer
		for _, e := range entries {
			b.WriteString("- ")
			b.WriteString(e)
			b.WriteString("\n")
		}
		entryPoints = b.String()
	}
	tmpl, err := template.New("s").Parse(summaryTpl)
	if err != nil {
		return err
	}
	data := SummaryData{
		LastIndex:      time.Now().Format(time.RFC3339),
		TotalFunctions: totalFuncs,
		TotalFiles:     totalFiles,
		DirSummary:     dirSummary,
		EntryPoints:    entryPoints,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "summary.md"), buf.Bytes(), 0644)
}

func ReadSummary(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "summary.md"))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
