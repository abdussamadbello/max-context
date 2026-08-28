package tools

import (
	"database/sql"

	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/mcp"
)

// readOnlyAnnotations marks a tool as a safe, repeatable, local-only read —
// every max-context tool queries the index and touches nothing else. Title is
// deliberately not set: it duplicates ToolSchema.Title in every definition.
func readOnlyAnnotations() *mcp.ToolAnnotations {
	t, f := true, false
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    &t,
		DestructiveHint: &f,
		IdempotentHint:  &t,
		OpenWorldHint:   &f,
	}
}

// Tool schemas are re-sent by the client on every request, so their size is a
// per-turn tax on every session — paid whether or not the tools are used. The
// text below is kept deliberately terse: it carries the cross-tool steering the
// A/B runs showed the model needs (which tool answers which question, and when
// to stop searching) and nothing else. Mechanics that the response itself
// explains do not belong here.
//
// Guard rail: TestToolSchemaBudget fails if the definitions grow past their
// budget, so this stays a decision rather than a drift.

// confidenceLevels is the shared resolution-confidence vocabulary for the two
// graph tools. Defined once: it appeared verbatim in both schemas before.
var confidenceLevels = []string{"interface-dispatch", "name-global", "receiver-typed", "same-package", "same-file"}

const confidenceDesc = "Minimum call-edge confidence to traverse. 'interface-dispatch' also follows interfaces to implementations (low confidence, off by default)."

func RegisterAll(h *mcp.Handler, database *sql.DB, q *db.Queries, projectRoot string) []mcp.ToolSchema {
	store := db.NewSQLiteStore(database)

	h.Register("get_definition", GetDefinitionHandler(database))
	h.Register("query_codebase", QueryCodebaseHandler(database, q, projectRoot))
	h.Register("get_call_chain", GetCallChainHandler(database))
	h.Register("get_impact", GetImpactHandler(store, projectRoot))
	h.Register("get_architecture", GetArchitectureHandler(database, projectRoot))

	str := func(desc string) map[string]string {
		return map[string]string{"type": "string", "description": desc}
	}
	depth := map[string]interface{}{"type": "integer", "description": "Recursion depth 1-5", "default": 2}
	confidence := map[string]interface{}{"type": "string", "description": confidenceDesc, "enum": confidenceLevels}

	return []mcp.ToolSchema{
		{
			Name:        "get_definition",
			Title:       "Find Definition",
			Annotations: readOnlyAnnotations(),
			Description: "Where is X defined? Exact name. Try this before query_codebase. When answer_status is 'definitive', answer immediately — do not search again.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"symbol": str("Exact symbol name")},
				"required":   []string{"symbol"},
			},
		},
		{
			Name:        "query_codebase",
			Title:       "Search Codebase",
			Annotations: readOnlyAnnotations(),
			Description: "Keyword search over indexed symbols and docs. Use get_definition for 'where is X defined', and get_impact or get_call_chain for 'what uses/breaks X'. One or two queries is usually enough; obey recommended_next_action.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":       str("Keywords or a symbol name"),
					"max_results": map[string]interface{}{"type": "integer", "description": "1-50", "default": 3},
					"scope":       map[string]interface{}{"type": "string", "description": "'docs' searches non-code files only", "enum": []string{"all", "functions", "types", "files", "docs"}, "default": "all"},
					"file_filter": str("Path glob, e.g. 'src/**/*.ts'"),
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "get_call_chain",
			Title:       "Trace Call Chain",
			Annotations: readOnlyAnnotations(),
			Description: "Who calls this function, and what does it call — recursively.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"function_name":  str("Function to trace"),
					"symbol":         str("Exact symbol from get_definition; disambiguates same-named methods"),
					"direction":      map[string]interface{}{"type": "string", "enum": []string{"callers", "callees", "both"}, "default": "both"},
					"depth":          depth,
					"max_results":    map[string]interface{}{"type": "integer", "description": "Cap per direction, nearest first (1-200)", "default": 50},
					"min_confidence": confidence,
				},
				"required": []string{},
			},
		},
		{
			Name:        "get_impact",
			Title:       "Analyze Change Impact",
			Annotations: readOnlyAnnotations(),
			Description: "What does a change break? Returns the blast radius of changed files. Defaults to the diff against HEAD. Use after editing to find affected tests and dependents.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"files":          map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}, "description": "Project-relative paths; defaults to the git diff"},
					"from_git":       str("Git revision, e.g. 'main..HEAD'. Ignored when files is set"),
					"depth":          depth,
					"direction":      map[string]interface{}{"type": "string", "enum": []string{"callers", "callees", "both"}, "default": "callers"},
					"include_tests":  map[string]interface{}{"type": "boolean", "default": true},
					"min_confidence": confidence,
					"token_budget":   map[string]interface{}{"type": "integer", "description": "Hard cap for the final JSON response (cl100k_base tokens)"},
				},
			},
		},
		{
			Name:        "get_architecture",
			Title:       "Project Architecture",
			Annotations: readOnlyAnnotations(),
			Description: "Project summary, modules, and entry points.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"focus": str("Optional subsystem filter")},
			},
		},
	}
}
