package tools

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/maxcontext/max-context/internal/mcp"
)

type getCallChainArgs struct {
	FunctionName string `json:"function_name"`
	Direction    string `json:"direction"`
	Depth        *int   `json:"depth"`
	// MinConfidence, when set, restricts traversal to edges at or above the given
	// resolution confidence. Like get_impact, the low-confidence interface-dispatch
	// fan-out is excluded by default and included only at a low min_confidence.
	MinConfidence string `json:"min_confidence"`
	// MaxResults caps each direction. Widely-called symbols produce enormous
	// answers otherwise: tracing callers of `Open` on this repo returned 3,272
	// tokens in one call, which is a poor trade for a tool whose value is
	// context efficiency.
	MaxResults *int `json:"max_results"`
}

// defaultCallChainResults caps each direction unless the caller asks for more.
// Deep enough to answer "who calls this" for almost every real symbol, small
// enough that a hub function cannot blow the context budget in one call.
const (
	defaultCallChainResults = 50
	maxCallChainResults     = 200
)

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
		limit := defaultCallChainResults
		if a.MaxResults != nil {
			limit = *a.MaxResults
			if limit < 1 {
				limit = 1
			}
			if limit > maxCallChainResults {
				limit = maxCallChainResults
			}
		}

		result := map[string]interface{}{
			"function": a.FunctionName,
			"depth":    depth,
		}
		truncated := false

		if direction == "callers" || direction == "both" {
			callers, err := queryCallChain(database, a.FunctionName, depth, "callers", a.MinConfidence)
			if err != nil {
				return nil, &mcp.RPCError{Code: mcp.CodeInternalError, Message: fmt.Sprintf("callers query failed: %v", err)}
			}
			kept, total := capNodes(callers, limit)
			result["callers"] = kept
			if total > len(kept) {
				truncated = true
				result["callers_total"] = total
			}
		}

		if direction == "callees" || direction == "both" {
			callees, err := queryCallChain(database, a.FunctionName, depth, "callees", a.MinConfidence)
			if err != nil {
				return nil, &mcp.RPCError{Code: mcp.CodeInternalError, Message: fmt.Sprintf("callees query failed: %v", err)}
			}
			kept, total := capNodes(callees, limit)
			result["callees"] = kept
			if total > len(kept) {
				truncated = true
				result["callees_total"] = total
			}
		}

		// Never truncate silently: an agent that cannot tell a capped answer from
		// a complete one will report a partial blast radius as the whole of it.
		if truncated {
			result["truncated"] = true
			result["recommended_next_action"] = actionNarrowScope
			result["note"] = fmt.Sprintf(
				"Results capped at %d per direction, nearest first. The totals above are exact. "+
					"To see more, raise max_results (up to %d); to see less and more precisely, "+
					"lower depth or raise min_confidence.", limit, maxCallChainResults)
		}

		attachStaleness(result, database)
		b, _ := json.Marshal(result)
		return []mcp.ContentItem{{Type: "text", Text: string(b)}}, nil
	}
}

func queryCallChain(database *sql.DB, functionName string, depth int, direction, minConfidence string) ([]callChainNode, error) {
	// edgeFilter gates which edges the walk may traverse by resolution confidence.
	// Default EXCLUDES the low-confidence interface-dispatch fan-out (so default
	// behavior is unchanged); a low min_confidence opts it in, a high one excludes
	// it along with other weak edges. Mirrors get_impact.
	edgeFilter := " AND e.resolution != 'interface-dispatch'"
	var filterArgs []interface{}
	if markers := edgeMarkersAtOrAbove(minConfidence); markers != nil {
		ph := make([]string, len(markers))
		for i, m := range markers {
			ph[i] = "?"
			filterArgs = append(filterArgs, m)
		}
		edgeFilter = " AND e.resolution IN (" + strings.Join(ph, ",") + ")"
	}

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
				WHERE c.depth < ?` + edgeFilter + `
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
				WHERE c.depth < ?` + edgeFilter + `
			)
			SELECT DISTINCT name, file_path, line, depth, resolution FROM chain
			WHERE depth > 0
			ORDER BY depth, name
		`
	}

	// Arg order: base-case name, recursive depth, then the edge-filter markers.
	args := append([]interface{}{functionName, depth}, filterArgs...)
	rows, err := database.Query(query, args...)
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

// capNodes trims a call-chain result to limit, returning the kept nodes and the
// true total. Rows arrive ordered by depth then name, so the nodes nearest the
// queried function are the ones kept.
func capNodes(nodes []callChainNode, limit int) ([]callChainNode, int) {
	total := len(nodes)
	if total <= limit {
		return nodes, total
	}
	return nodes[:limit], total
}
