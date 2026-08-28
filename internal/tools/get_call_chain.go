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
	// Symbol addresses ONE definition precisely, where function_name matches
	// every definition sharing that name. A repository with EmailNotifier.Send,
	// SMSNotifier.Send and MetricsBuffer.Send cannot be asked about one of them
	// by name; get_definition returns each match's symbol for this argument.
	Symbol    string `json:"symbol"`
	Direction string `json:"direction"`
	Depth     *int   `json:"depth"`
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
		if a.FunctionName == "" && a.Symbol == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "function_name or symbol required"}
		}
		// A symbol names exactly one definition, so it supersedes the name. When
		// it matches nothing, say so rather than silently widening to every
		// same-named definition — that would answer a different question than
		// the one asked, which is the failure this argument exists to fix.
		seed, seedBySymbol := a.FunctionName, false
		if a.Symbol != "" {
			name, ok := nameForSymbol(database, a.Symbol)
			if !ok {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: fmt.Sprintf(
					"no definition has symbol %q; call get_definition to get the symbol of the definition you mean", a.Symbol)}
			}
			seed, seedBySymbol = a.Symbol, true
			a.FunctionName = name
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
		if seedBySymbol {
			result["symbol"] = a.Symbol
		}
		truncated := false

		if direction == "callers" || direction == "both" {
			callers, err := queryCallChain(database, seed, depth, "callers", a.MinConfidence, seedBySymbol)
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
			callees, err := queryCallChain(database, seed, depth, "callees", a.MinConfidence, seedBySymbol)
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

		// Never exclude silently either. The default filter drops the
		// interface-dispatch fan-out, so a method reached only through an
		// interface returns no callers at all — indistinguishable from having
		// none. Worse, when an unrelated type shares the method name, the one
		// row that survives is the wrong one. Say the edges exist and name the
		// argument that shows them.
		if edgeMarkersAtOrAbove(a.MinConfidence) == nil {
			if n := countInterfaceDispatchEdges(database, a.FunctionName, direction); n > 0 {
				result["interface_dispatch_excluded"] = n
				result["interface_dispatch_hint"] = fmt.Sprintf(
					"%d edge(s) reach %s through an interface whose fan-out is too wide (>%d "+
						"implementations) to include by default. Re-run with min_confidence "+
						"\"interface-dispatch\" to include them.", n, a.FunctionName, maxDefaultDispatchWidth)
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

// countInterfaceDispatchEdges counts the interface-dispatch edges the default
// confidence filter still hides for one symbol — the wide fan-outs, and rows
// from an index predating dispatch_width. Narrow sites are admitted by default
// now and must not be reported as hidden. Direct edges only: this answers "is
// there something behind the filter", not "how large is the fan-out at depth",
// and a recursive walk to phrase a hint would cost more than the hint saves.
func countInterfaceDispatchEdges(database *sql.DB, functionName, direction string) int {
	const dispatch = "interface-dispatch"
	hidden := fmt.Sprintf(" AND (e.dispatch_width = 0 OR e.dispatch_width > %d)", maxDefaultDispatchWidth)
	var q string
	switch direction {
	case "callers":
		q = `SELECT COUNT(*) FROM calls e JOIN functions f ON f.id = e.callee_id
		     WHERE f.name = ? AND e.resolution = ?` + hidden
	case "callees":
		q = `SELECT COUNT(*) FROM calls e JOIN functions f ON f.id = e.caller_id
		     WHERE f.name = ? AND e.resolution = ?` + hidden
	default: // both
		q = `SELECT COUNT(*) FROM calls e
		     WHERE e.resolution = ? AND (
		       e.callee_id IN (SELECT id FROM functions WHERE name = ?)
		       OR e.caller_id IN (SELECT id FROM functions WHERE name = ?))` + hidden
		var n int
		if err := database.QueryRow(q, dispatch, functionName, functionName).Scan(&n); err != nil {
			return 0
		}
		return n
	}
	var n int
	if err := database.QueryRow(q, functionName, dispatch).Scan(&n); err != nil {
		return 0
	}
	return n
}

// queryCallChain walks the graph from a seed definition. seedBySymbol selects
// which column the base case matches: resolving a symbol to its name and
// seeding on the name would re-widen to every same-named definition, undoing
// the precision the symbol was passed for.
func queryCallChain(database *sql.DB, seed string, depth int, direction, minConfidence string, seedBySymbol bool) ([]callChainNode, error) {
	seedClause := "f.name = ?"
	if seedBySymbol {
		seedClause = "f.symbol = ?"
	}
	// edgeFilter gates which edges the walk may traverse by resolution confidence.
	// The default admits interface-dispatch edges only where the call site's
	// fan-out is narrow, and excludes the wide ones; a low min_confidence opts
	// into all of them, a high one excludes them along with other weak edges.
	// Mirrors get_impact.
	edgeFilter := defaultEdgeFilter()
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
				FROM functions f WHERE ` + seedClause + `
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
				FROM functions f WHERE ` + seedClause + `
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

	// Arg order: base-case seed, recursive depth, then the edge-filter markers.
	args := append([]interface{}{seed, depth}, filterArgs...)
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

// maxDefaultDispatchWidth is how many concrete implementations an interface
// call site may fan out to before its edges are excluded at the default
// confidence.
//
// Chosen from the fan-out distribution measured on cobra, gin, and
// client_golang: widths cluster at 1–5 (77% of call sites) and then jump to 13
// and 19, with nothing in between. Admitting the narrow cluster recovers the
// callers of an interface method — previously absent entirely, so a method
// reached only through an interface looked like it had none — while the wide
// sites, which grew responses by 872% and 1138%, stay behind min_confidence.
const maxDefaultDispatchWidth = 5

// defaultEdgeFilter is the SQL predicate applied when no min_confidence is
// given. dispatch_width is 0 on rows indexed before the column existed, so an
// un-reindexed database keeps the old exclude-everything behaviour rather than
// silently widening.
func defaultEdgeFilter() string {
	return fmt.Sprintf(
		" AND (e.resolution != 'interface-dispatch' OR (e.dispatch_width > 0 AND e.dispatch_width <= %d))",
		maxDefaultDispatchWidth)
}

// nameForSymbol returns the definition name a symbol addresses. Used only to
// label the response and to verify the symbol exists — the walk itself seeds on
// the symbol, so an unknown one must fail loudly rather than fall back to a
// name match that would answer about every same-named definition.
func nameForSymbol(database *sql.DB, symbol string) (string, bool) {
	var name string
	err := database.QueryRow(`SELECT name FROM functions WHERE symbol = ? LIMIT 1`, symbol).Scan(&name)
	if err != nil {
		return "", false
	}
	return name, true
}
