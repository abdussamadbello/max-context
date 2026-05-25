package tools

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/gitdiff"
	"github.com/maxcontext/max-context/internal/mcp"
)

const maxImpactNodes = 1000

type getImpactArgs struct {
	Files        []string `json:"files"`
	FromGit      string   `json:"from_git"`
	Depth        *int     `json:"depth"`
	Direction    string   `json:"direction"`
	IncludeTests *bool    `json:"include_tests"`
}

type changedFile struct {
	File    string   `json:"file"`
	Symbols []string `json:"symbols"`
}

type impactedNode struct {
	File   string `json:"file"`
	Symbol string `json:"symbol"`
	Line   int    `json:"line"`
	Depth  int    `json:"depth"`
	Via    string `json:"via"`
	Kind   string `json:"kind"`
}

type impactStats struct {
	ChangedFiles    int  `json:"changed_files"`
	ChangedSymbols  int  `json:"changed_symbols"`
	ImpactedSymbols int  `json:"impacted_symbols"`
	MaxDepthReached int  `json:"max_depth_reached"`
	Truncated       bool `json:"truncated"`
}

type impactResponse struct {
	Changed         []changedFile  `json:"changed"`
	Impacted        []impactedNode `json:"impacted"`
	UnresolvedFiles []string       `json:"unresolved_files"`
	Stats           impactStats    `json:"stats"`
}

// GetImpactHandler returns the MCP tool handler. The store provides SymbolsInFile and
// raw DB access (for the recursive CTE). projectRoot is used as the git working directory.
func GetImpactHandler(store db.Store, projectRoot string) mcp.ToolHandler {
	return func(args json.RawMessage) (interface{}, error) {
		var a getImpactArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
			}
		}

		depth := 2
		if a.Depth != nil {
			depth = *a.Depth
			if depth < 1 {
				depth = 1
			}
			if depth > 5 {
				depth = 5
			}
		}
		direction := a.Direction
		if direction == "" {
			direction = "callers"
		}
		includeTests := true
		if a.IncludeTests != nil {
			includeTests = *a.IncludeTests
		}

		files := a.Files
		if len(files) == 0 {
			rev := a.FromGit
			if rev == "" {
				rev = "HEAD"
			}
			diffed, err := gitdiff.Diff(projectRoot, rev)
			if err != nil {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
			}
			files = diffed
		}

		var changed []changedFile
		var unresolved []string
		seedIDs := map[int64]string{}
		seedSymbols := []string{}
		for _, f := range files {
			syms, err := store.SymbolsInFile(f)
			if err != nil {
				return nil, &mcp.RPCError{Code: mcp.CodeInternalError, Message: fmt.Sprintf("SymbolsInFile %q: %v", f, err)}
			}
			if len(syms) == 0 {
				unresolved = append(unresolved, f)
				continue
			}
			names := make([]string, 0, len(syms))
			for _, s := range syms {
				seedIDs[s.ID] = s.Name
				seedSymbols = append(seedSymbols, s.Name)
				names = append(names, s.Name)
			}
			changed = append(changed, changedFile{File: f, Symbols: names})
		}
		if unresolved == nil {
			unresolved = []string{}
		}
		if changed == nil {
			changed = []changedFile{}
		}

		impacted, truncated, maxDepth, err := queryImpact(store.DB(), seedIDs, depth, direction, includeTests)
		if err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInternalError, Message: fmt.Sprintf("impact query: %v", err)}
		}

		resp := impactResponse{
			Changed:         changed,
			Impacted:        impacted,
			UnresolvedFiles: unresolved,
			Stats: impactStats{
				ChangedFiles:    len(changed),
				ChangedSymbols:  len(seedSymbols),
				ImpactedSymbols: len(impacted),
				MaxDepthReached: maxDepth,
				Truncated:       truncated,
			},
		}
		b, _ := json.Marshal(resp)
		return []mcp.ContentItem{{Type: "text", Text: string(b)}}, nil
	}
}

func queryImpact(database *sql.DB, seedIDs map[int64]string, depth int, direction string, includeTests bool) ([]impactedNode, bool, int, error) {
	if len(seedIDs) == 0 {
		return []impactedNode{}, false, 0, nil
	}

	ids := make([]interface{}, 0, len(seedIDs))
	placeholders := make([]string, 0, len(seedIDs))
	for id := range seedIDs {
		ids = append(ids, id)
		placeholders = append(placeholders, "?")
	}

	collect := func(walkSQL string) ([]impactedNode, int, error) {
		query := fmt.Sprintf(`
			WITH RECURSIVE chain(id, name, file_path, line, depth, via_id) AS (
				SELECT f.id, f.name, f.file_path, f.start_line, 0, f.id
				FROM functions f WHERE f.id IN (%s)
				UNION ALL
				%s
			)
			SELECT chain.id, chain.name, chain.file_path, chain.line, chain.depth,
			       seed.name AS via_name
			FROM chain
			JOIN functions seed ON seed.id = chain.via_id
			WHERE chain.depth > 0
			ORDER BY chain.depth, chain.name
			LIMIT ?
		`, strings.Join(placeholders, ","), walkSQL)
		args := append([]interface{}{}, ids...)
		args = append(args, depth, maxImpactNodes+1)
		rows, err := database.Query(query, args...)
		if err != nil {
			return nil, 0, err
		}
		defer rows.Close()
		out := []impactedNode{}
		seen := map[int64]int{}
		maxDepthSeen := 0
		for rows.Next() {
			var id int64
			var n impactedNode
			var via string
			if err := rows.Scan(&id, &n.Symbol, &n.File, &n.Line, &n.Depth, &via); err != nil {
				return nil, 0, err
			}
			n.Via = via
			n.Kind = "function"
			if !includeTests && isTestFile(n.File) {
				continue
			}
			if idx, ok := seen[id]; ok {
				if n.Depth < out[idx].Depth {
					out[idx] = n
				}
				continue
			}
			seen[id] = len(out)
			out = append(out, n)
			if n.Depth > maxDepthSeen {
				maxDepthSeen = n.Depth
			}
		}
		return out, maxDepthSeen, rows.Err()
	}

	callersWalk := `
		SELECT f.id, f.name, f.file_path, f.start_line, chain.depth + 1, chain.via_id
		FROM chain
		JOIN calls e ON e.callee_id = chain.id
		JOIN functions f ON f.id = e.caller_id
		WHERE chain.depth < ? AND f.id != chain.id
	`
	calleesWalk := `
		SELECT f.id, f.name, f.file_path, f.start_line, chain.depth + 1, chain.via_id
		FROM chain
		JOIN calls e ON e.caller_id = chain.id
		JOIN functions f ON f.id = e.callee_id
		WHERE chain.depth < ? AND f.id != chain.id
	`

	var combined []impactedNode
	var maxDepth int
	switch direction {
	case "callees":
		rows, d, err := collect(calleesWalk)
		if err != nil {
			return nil, false, 0, err
		}
		combined = rows
		maxDepth = d
	case "both":
		up, d1, err := collect(callersWalk)
		if err != nil {
			return nil, false, 0, err
		}
		down, d2, err := collect(calleesWalk)
		if err != nil {
			return nil, false, 0, err
		}
		seen := map[string]bool{}
		for _, n := range up {
			key := fmt.Sprintf("%s|%s", n.File, n.Symbol)
			if !seen[key] {
				combined = append(combined, n)
				seen[key] = true
			}
		}
		for _, n := range down {
			key := fmt.Sprintf("%s|%s", n.File, n.Symbol)
			if !seen[key] {
				combined = append(combined, n)
				seen[key] = true
			}
		}
		if d1 > d2 {
			maxDepth = d1
		} else {
			maxDepth = d2
		}
	default: // "callers"
		rows, d, err := collect(callersWalk)
		if err != nil {
			return nil, false, 0, err
		}
		combined = rows
		maxDepth = d
	}

	truncated := false
	if len(combined) > maxImpactNodes {
		combined = combined[:maxImpactNodes]
		truncated = true
	}
	return combined, truncated, maxDepth, nil
}

// isTestFile applies a conservative test-file naming rule across the supported langs.
func isTestFile(path string) bool {
	p := strings.ToLower(path)
	switch {
	case strings.HasSuffix(p, "_test.go"):
		return true
	case strings.HasSuffix(p, ".test.ts"), strings.HasSuffix(p, ".test.tsx"),
		strings.HasSuffix(p, ".test.js"), strings.HasSuffix(p, ".test.jsx"):
		return true
	case strings.HasSuffix(p, ".spec.ts"), strings.HasSuffix(p, ".spec.tsx"),
		strings.HasSuffix(p, ".spec.js"), strings.HasSuffix(p, ".spec.jsx"):
		return true
	case strings.HasPrefix(p, "test_") || strings.Contains(p, "/test_"):
		return true
	case strings.HasSuffix(p, "_test.py"):
		return true
	}
	return false
}
