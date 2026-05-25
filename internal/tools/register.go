package tools

import (
	"database/sql"

	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/mcp"
)

func RegisterAll(h *mcp.Handler, database *sql.DB, q *db.Queries, projectRoot string) []mcp.ToolSchema {
	store := db.NewSQLiteStore(database)

	h.Register("query_codebase", QueryCodebaseHandler(database, q, projectRoot))
	h.Register("get_call_chain", GetCallChainHandler(database))
	h.Register("get_impact", GetImpactHandler(store, projectRoot))
	h.Register("get_architecture", GetArchitectureHandler(projectRoot))

	return []mcp.ToolSchema{
		{
			Name:        "query_codebase",
			Description: "Search the indexed codebase for functions, types, or files by keyword. Returns BM25-ranked results with file paths, line numbers, and code snippets. Returns 'suggestions' when results are weak.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":       map[string]string{"type": "string", "description": "Search query (keywords or function/type name)"},
					"max_results": map[string]interface{}{"type": "integer", "description": "Max results to return (1-50)", "default": 5},
					"scope":       map[string]interface{}{"type": "string", "description": "Restrict search scope", "enum": []string{"all", "functions", "types", "files"}, "default": "all"},
					"file_filter": map[string]interface{}{"type": "string", "description": "Glob pattern to filter by file path (e.g. 'src/**/*.ts')"},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "get_call_chain",
			Description: "Traverse the call graph to find who calls a function (callers) and what it calls (callees), recursively up to a configurable depth.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"function_name": map[string]string{"type": "string", "description": "Name of the function to trace"},
					"direction":     map[string]interface{}{"type": "string", "description": "Traversal direction", "enum": []string{"callers", "callees", "both"}, "default": "both"},
					"depth":         map[string]interface{}{"type": "integer", "description": "Max recursion depth (1-5)", "default": 2},
				},
				"required": []string{"function_name"},
			},
		},
		{
			Name:        "get_impact",
			Description: "Given changed files (or a git rev range), return symbols whose blast radius is affected. Default behaviour: diff against HEAD, walk callers to depth 2. Use this post-edit to learn what tests and dependents your change may break.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"files":         map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}, "description": "Explicit file list (project-root relative). If omitted, uses from_git or defaults to HEAD."},
					"from_git":      map[string]string{"type": "string", "description": "Git revision (e.g. 'HEAD' or 'main..HEAD'). Ignored if 'files' is set."},
					"depth":         map[string]interface{}{"type": "integer", "description": "Max recursion depth (1-5)", "default": 2},
					"direction":     map[string]interface{}{"type": "string", "description": "callers (blast radius), callees (dependencies), or both", "enum": []string{"callers", "callees", "both"}, "default": "callers"},
					"include_tests": map[string]interface{}{"type": "boolean", "description": "Include test files in results", "default": true},
				},
			},
		},
		{
			Name:        "get_architecture",
			Description: "Return the project's architecture summary, modules, and entry points.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"focus": map[string]string{"type": "string", "description": "Optional subsystem filter"},
				},
			},
		},
	}
}
