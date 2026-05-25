package artifacts

import (
	"bytes"
	"database/sql"
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
	DirSummary    string
	EntryPoints   string
}

func WriteSummary(dir string, database *sql.DB) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	var totalFuncs, totalFiles int
	_ = database.QueryRow("SELECT COUNT(*) FROM functions").Scan(&totalFuncs)
	_ = database.QueryRow("SELECT COUNT(DISTINCT file_path) FROM functions").Scan(&totalFiles)
	rows, _ := database.Query("SELECT file_path FROM functions GROUP BY file_path ORDER BY file_path LIMIT 20")
	var dirs []string
	if rows != nil {
		for rows.Next() {
			var p string
			if rows.Scan(&p) == nil {
				dirs = append(dirs, p)
			}
		}
		rows.Close()
	}
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
	entryRows, _ := database.Query("SELECT DISTINCT file_path FROM functions WHERE file_path LIKE '%main.%' OR file_path LIKE '%index.%' LIMIT 10")
	var entries []string
	if entryRows != nil {
		for entryRows.Next() {
			var p string
			if entryRows.Scan(&p) == nil {
				entries = append(entries, p)
			}
		}
		entryRows.Close()
	}
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
