package tools

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maxcontext/max-context/internal/artifacts"
	"github.com/maxcontext/max-context/internal/mcp"
)

type getArchitectureArgs struct {
	Focus string `json:"focus"`
}

func GetArchitectureHandler(database *sql.DB, projectRoot string) mcp.ToolHandler {
	dir := filepath.Join(projectRoot, ".max-context")
	return func(args json.RawMessage) (interface{}, error) {
		var a getArchitectureArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
			}
		}
		summary, err := artifacts.ReadSummary(dir)
		if err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeIndexNotReady, Message: "architecture not ready; run index first"}
		}
		text := summary
		if focus := strings.TrimSpace(a.Focus); focus != "" {
			focused, err := focusedArchitecture(database, focus)
			if err != nil {
				return nil, &mcp.RPCError{Code: mcp.CodeInternalError, Message: "build focused architecture: " + err.Error()}
			}
			text = focused
		} else {
			arch, _ := artifacts.ReadArchitecture(dir)
			if arch != "" {
				text = summary + "\n\n" + arch
			}
		}
		// get_architecture returns prose (not JSON), so surface staleness as an
		// inline warning line when the index is unhealthy — keeping the text shape
		// backward compatible.
		if _, warning := stalenessInfo(database); warning != "" {
			text += "\n\n[staleness] " + warning
		}
		return []mcp.ContentItem{{Type: "text", Text: text}}, nil
	}
}

// focusedArchitecture derives a compact subsystem view from live indexed data.
// It intentionally does not filter the precomputed markdown: directory-level
// rows such as "internal/" lose the detail needed to focus on "indexer".
func focusedArchitecture(database *sql.DB, focus string) (string, error) {
	rows, err := database.Query(`
		SELECT file_path FROM file_summaries
		UNION SELECT file_path FROM functions
		UNION SELECT file_path FROM types
		UNION SELECT file_path FROM documents
		UNION SELECT file_path FROM imports`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var files []string
	for rows.Next() {
		var file string
		if err := rows.Scan(&file); err != nil {
			return "", err
		}
		if architectureFocusMatch(file, focus) {
			files = append(files, filepath.ToSlash(file))
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	sort.Strings(files)

	var b strings.Builder
	fmt.Fprintf(&b, "# Architecture focus: %s\n\n", focus)
	if len(files) == 0 {
		b.WriteString("No indexed files matched this subsystem.\n")
		return b.String(), nil
	}
	fmt.Fprintf(&b, "**Matched files:** %d\n\n## Files\n\n", len(files))
	const maxFiles = 40
	for i, file := range files {
		if i == maxFiles {
			fmt.Fprintf(&b, "- … and %d more\n", len(files)-maxFiles)
			break
		}
		fmt.Fprintf(&b, "- %s\n", file)
	}

	ranked, err := artifacts.PageRankFunctions(database)
	if err != nil {
		return "", err
	}
	b.WriteString("\n## Key functions (PageRank)\n\n")
	keyCount := 0
	for _, fn := range ranked {
		if !architectureFocusMatch(fn.FilePath, focus) && !architectureFocusMatch(fn.Name, focus) {
			continue
		}
		fmt.Fprintf(&b, "- %s (%s)\n", fn.Name, fn.FilePath)
		keyCount++
		if keyCount == 15 {
			break
		}
	}
	if keyCount == 0 {
		b.WriteString("- None.\n")
	}

	b.WriteString("\n## Module dependencies\n\n")
	depCount := 0
	for _, file := range files {
		depRows, err := database.Query(`SELECT DISTINCT imported_path FROM imports WHERE file_path = ? ORDER BY imported_path`, file)
		if err != nil {
			return "", err
		}
		for depRows.Next() {
			var imported string
			if err := depRows.Scan(&imported); err != nil {
				depRows.Close()
				return "", err
			}
			fmt.Fprintf(&b, "- %s -> %s\n", file, imported)
			depCount++
			if depCount == 30 {
				break
			}
		}
		if err := depRows.Err(); err != nil {
			depRows.Close()
			return "", err
		}
		depRows.Close()
		if depCount == 30 {
			break
		}
	}
	if depCount == 0 {
		b.WriteString("- None indexed.\n")
	}
	return b.String(), nil
}

func architectureFocusMatch(value, focus string) bool {
	value = strings.ToLower(filepath.ToSlash(value))
	focus = strings.ToLower(filepath.ToSlash(strings.TrimSpace(focus)))
	return focus != "" && strings.Contains(value, focus)
}
