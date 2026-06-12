package tools

import (
	"database/sql"

	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/mcp"
)

func RegisterAll(h *mcp.Handler, database *sql.DB, q *db.Queries, projectRoot string) []mcp.ToolSchema {
	store := db.NewSQLiteStore(database)

	h.Register("get_definition", GetDefinitionHandler(database))
	h.Register("query_codebase", QueryCodebaseHandler(database, q, projectRoot))
	h.Register("get_call_chain", GetCallChainHandler(database))
	h.Register("get_impact", GetImpactHandler(store, projectRoot))
	h.Register("get_architecture", GetArchitectureHandler(database, projectRoot))

	return []mcp.ToolSchema{
		{
			Name:        "get_definition",
			Description: "Find where a symbol is defined by EXACT name. Use this first for 'where is X defined?' questions. Returns answer_status, recommended_next_action, and a canonical result when a class/type should outrank same-named methods or properties. If the result is definitive, answer immediately without further searching.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbol": map[string]string{"type": "string", "description": "Exact symbol name (function, method, type, class)"},
				},
				"required": []string{"symbol"},
			},
		},
		{
			Name:        "query_codebase",
			Description: "Fuzzy/keyword search of the indexed codebase for functions, types, and docs/config files (markdown, YAML, JSON, proto, GraphQL, SQL, Dockerfiles). Returns terse ranked results plus answer_status and recommended_next_action. For overloaded exact names, canonical type/class definitions outrank same-named methods/properties. For 'where is X defined?' prefer get_definition; for dependency/usage questions prefer get_impact or get_call_chain. One or two queries is usually enough.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":       map[string]string{"type": "string", "description": "Search query (keywords or function/type name)"},
					"max_results": map[string]interface{}{"type": "integer", "description": "Max results to return (1-50)", "default": 3},
					"scope":       map[string]interface{}{"type": "string", "description": "Restrict search scope. 'docs' searches non-code files (markdown, YAML, JSON, proto, GraphQL, SQL, Dockerfiles) only.", "enum": []string{"all", "functions", "types", "files", "docs"}, "default": "all"},
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
					"function_name":  map[string]string{"type": "string", "description": "Name of the function to trace"},
					"direction":      map[string]interface{}{"type": "string", "description": "Traversal direction", "enum": []string{"callers", "callees", "both"}, "default": "both"},
					"depth":          map[string]interface{}{"type": "integer", "description": "Max recursion depth (1-5)", "default": 2},
					"min_confidence": map[string]interface{}{"type": "string", "description": "Only traverse call edges at or above this resolution confidence. Set 'interface-dispatch' to ALSO follow interface methods to concrete implementations (low-confidence, off by default).", "enum": []string{"interface-dispatch", "name-global", "receiver-typed", "same-package", "same-file"}},
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
					"files":          map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}, "description": "Explicit file list (project-root relative). If omitted, uses from_git or defaults to HEAD."},
					"from_git":       map[string]string{"type": "string", "description": "Git revision (e.g. 'HEAD' or 'main..HEAD'). Ignored if 'files' is set."},
					"depth":          map[string]interface{}{"type": "integer", "description": "Max recursion depth (1-5)", "default": 2},
					"direction":      map[string]interface{}{"type": "string", "description": "callers (blast radius), callees (dependencies), or both", "enum": []string{"callers", "callees", "both"}, "default": "callers"},
					"include_tests":  map[string]interface{}{"type": "boolean", "description": "Include test files in results", "default": true},
					"min_confidence": map[string]interface{}{"type": "string", "description": "Only traverse call edges at or above this resolution confidence. Use to exclude lower-confidence guesses from the blast radius. Set 'interface-dispatch' to ALSO fan out through interface methods to concrete implementations (low-confidence, off by default).", "enum": []string{"interface-dispatch", "name-global", "receiver-typed", "same-package", "same-file"}},
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
