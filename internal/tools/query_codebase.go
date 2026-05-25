package tools

import (
	"database/sql"
	"encoding/json"

	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/mcp"
)

type queryCodebaseArgs struct {
	Query      string  `json:"query"`
	MaxResults *int    `json:"max_results"`
	Limit      *int    `json:"limit"` // backward compat alias
	Scope      string  `json:"scope"`
	FileFilter *string `json:"file_filter"`
}

type searchResult struct {
	File    string   `json:"file"`
	Line    int      `json:"line"`
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	Snippet string   `json:"snippet,omitempty"`
	Callers []string `json:"callers,omitempty"`
	Callees []string `json:"callees,omitempty"`
}

func QueryCodebaseHandler(database *sql.DB, q *db.Queries, projectRoot string) mcp.ToolHandler {
	return func(args json.RawMessage) (interface{}, error) {
		var a queryCodebaseArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
			}
		}
		if a.Query == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "query required"}
		}
		limit := 5
		if a.MaxResults != nil {
			limit = *a.MaxResults
		} else if a.Limit != nil {
			limit = *a.Limit
		}
		if limit < 1 {
			limit = 1
		}
		if limit > 50 {
			limit = 50
		}
		scope := a.Scope
		if scope == "" {
			scope = "all"
		}

		var results []searchResult
		var functionIDs []int64 // for enrichment

		if scope == "all" || scope == "functions" {
			rows, err := q.SearchFunctions.Query(a.Query, limit)
			if err != nil {
				return nil, &mcp.RPCError{Code: mcp.CodeIndexNotReady, Message: "index not ready: run /index-codebase first"}
			}
			for rows.Next() {
				var id int64
				var name, filePath string
				var startLine, endLine int
				var language string
				var exported int
				var code, docstring, signature, snippet sql.NullString
				var rank float64
				if rows.Scan(&id, &name, &filePath, &startLine, &endLine, &language, &exported, &code, &docstring, &signature, &snippet, &rank) == nil {
					results = append(results, searchResult{
						File: filePath, Line: startLine, Kind: "function", Name: name, Snippet: snippet.String,
					})
					functionIDs = append(functionIDs, id)
				}
			}
			rows.Close()
		}
		if scope == "all" || scope == "types" {
			rows, err := q.SearchTypes.Query(a.Query, limit)
			if err == nil {
				for rows.Next() {
					var id int64
					var name, filePath, kind string
					var definition string
					var exported int
					var snippet sql.NullString
					var rank float64
					if rows.Scan(&id, &name, &filePath, &kind, &definition, &exported, &snippet, &rank) == nil {
						results = append(results, searchResult{
							File: filePath, Kind: "type/" + kind, Name: name, Snippet: snippet.String,
						})
					}
				}
				rows.Close()
			}
		}

		// 1-hop enrichment: attach caller/callee names for function results
		for i, id := range functionIDs {
			if callers, err := getNames(q.GetCallersOf, id); err == nil && len(callers) > 0 {
				results[i].Callers = callers
			}
			if callees, err := getNames(q.GetCalleesOf, id); err == nil && len(callees) > 0 {
				results[i].Callees = callees
			}
		}

		b, _ := json.Marshal(map[string]interface{}{"results": results, "total": len(results)})
		return []mcp.ContentItem{{Type: "text", Text: string(b)}}, nil
	}
}

// getNames executes a prepared statement that returns a single name column per row.
func getNames(stmt *sql.Stmt, id int64) ([]string, error) {
	rows, err := stmt.Query(id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil {
			names = append(names, name)
		}
	}
	return names, nil
}
