package tools

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/maxcontext/max-context/internal/mcp"
)

type getCallChainArgs struct {
	FunctionName string `json:"function_name"`
	Direction    string `json:"direction"`
	Depth        *int   `json:"depth"`
}

type callChainNode struct {
	Name       string `json:"name"`
	FilePath   string `json:"file_path"`
	Line       *int   `json:"line"`
	Depth      int    `json:"depth"`
	Resolution string `json:"resolution"`
}

// GetCallChainHandler returns an MCP tool handler that traverses the call graph
// using recursive CTEs in SQLite. Supports callers (upstream), callees (downstream),
// or both directions.
func GetCallChainHandler(database *sql.DB) mcp.ToolHandler {
	return func(args json.RawMessage) (interface{}, error) {
		var a getCallChainArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
			}
		}
		if a.FunctionName == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "function_name required"}
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
			direction = "both"
		}

		result := map[string]interface{}{
			"function": a.FunctionName,
			"depth":    depth,
		}

		if direction == "callers" || direction == "both" {
			callers, err := queryCallChain(database, a.FunctionName, depth, "callers")
			if err != nil {
				return nil, &mcp.RPCError{Code: mcp.CodeInternalError, Message: fmt.Sprintf("callers query failed: %v", err)}
			}
			result["callers"] = callers
		}

		if direction == "callees" || direction == "both" {
			callees, err := queryCallChain(database, a.FunctionName, depth, "callees")
			if err != nil {
				return nil, &mcp.RPCError{Code: mcp.CodeInternalError, Message: fmt.Sprintf("callees query failed: %v", err)}
			}
			result["callees"] = callees
		}

		attachStaleness(result, database)
		b, _ := json.Marshal(result)
		return []mcp.ContentItem{{Type: "text", Text: string(b)}}, nil
	}
}

func queryCallChain(database *sql.DB, functionName string, depth int, direction string) ([]callChainNode, error) {
	var query string
	if direction == "callers" {
		// Who calls this function? (upstream). resolution is the confidence of
		// the edge connecting each caller to the chain; the seed row has none.
		query = `
			WITH RECURSIVE chain(id, name, file_path, line, depth, resolution) AS (
				SELECT f.id, f.name, f.file_path, f.start_line, 0, ''
				FROM functions f WHERE f.name = ?
				UNION ALL
				SELECT f.id, f.name, f.file_path, f.start_line, c.depth + 1, e.resolution
				FROM chain c
				JOIN calls e ON e.callee_id = c.id
				JOIN functions f ON f.id = e.caller_id
				WHERE c.depth < ?
			)
			SELECT DISTINCT name, file_path, line, depth, resolution FROM chain
			WHERE depth > 0
			ORDER BY depth, name
		`
	} else {
		// What does this function call? (downstream)
		query = `
			WITH RECURSIVE chain(id, name, file_path, line, depth, resolution) AS (
				SELECT f.id, f.name, f.file_path, f.start_line, 0, ''
				FROM functions f WHERE f.name = ?
				UNION ALL
				SELECT COALESCE(f.id, 0), COALESCE(f.name, e.callee_name), COALESCE(f.file_path, '(external)'), f.start_line, c.depth + 1, e.resolution
				FROM chain c
				JOIN calls e ON e.caller_id = c.id
				LEFT JOIN functions f ON f.id = e.callee_id
				WHERE c.depth < ?
			)
			SELECT DISTINCT name, file_path, line, depth, resolution FROM chain
			WHERE depth > 0
			ORDER BY depth, name
		`
	}

	rows, err := database.Query(query, functionName, depth)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []callChainNode
	for rows.Next() {
		var n callChainNode
		var line sql.NullInt64
		if err := rows.Scan(&n.Name, &n.FilePath, &line, &n.Depth, &n.Resolution); err != nil {
			continue
		}
		if line.Valid {
			l := int(line.Int64)
			n.Line = &l
		}
		nodes = append(nodes, n)
	}
	if nodes == nil {
		nodes = []callChainNode{}
	}
	return nodes, nil
}
