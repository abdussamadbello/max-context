package tools

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/maxcontext/max-context/internal/contextpack"
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
	// MinConfidence, when set, restricts the walk to edges at or above the given
	// resolution confidence (e.g. "receiver-typed" excludes name-global guesses).
	MinConfidence string `json:"min_confidence"`
	// TokenBudget, when set, caps the complete serialized JSON response using the
	// shared cl100k_base budget profile. An omitted value preserves the original
	// uncapped response contract.
	TokenBudget *int `json:"token_budget"`
}

// resolutionRank orders resolution markers by confidence (higher = stronger).
// Linking markers only; classify-only/miss markers never produce a traversable
// edge so they are absent here.
var resolutionRank = map[string]int{
	"same-file":          5,
	"same-package":       4,
	"receiver-typed":     3,
	"interface-dispatch": 2, // low-confidence interface fan-out; included only at low min_confidence
	"name-global":        1,
}

// edgeMarkersAtOrAbove returns the resolution markers with confidence >= the
// named level. Returns nil (no filtering) when level is empty or unknown.
func edgeMarkersAtOrAbove(level string) []string {
	min, ok := resolutionRank[level]
	if !ok {
		return nil
	}
	var out []string
	for m, r := range resolutionRank {
		if r >= min {
			out = append(out, m)
		}
	}
	return out
}

type changedFile struct {
	File    string   `json:"file"`
	Symbols []string `json:"symbols"`
}

type impactedNode struct {
	File          string `json:"file"`
	Symbol        string `json:"symbol"`
	Line          int    `json:"line"`
	Depth         int    `json:"depth"`
	Via           string `json:"via"`
	Kind          string `json:"kind"`
	ViaResolution string `json:"via_resolution"`
}

type impactStats struct {
	ChangedFiles          int            `json:"changed_files"`
	ChangedSymbols        int            `json:"changed_symbols"`
	ImpactedSymbols       int            `json:"impacted_symbols"`
	ReturnedImpactSymbols int            `json:"returned_impacted_symbols,omitempty"`
	MaxDepthReached       int            `json:"max_depth_reached"`
	Truncated             bool           `json:"truncated"`
	ResolutionBreakdown   map[string]int `json:"resolution_breakdown"`
}

type impactOmitted struct {
	Impacted               int  `json:"impacted"`
	BeyondGraphSafetyLimit bool `json:"beyond_graph_safety_limit,omitempty"`
}

type impactResponse struct {
	Changed          []changedFile          `json:"changed"`
	Impacted         []impactedNode         `json:"impacted"`
	UnresolvedFiles  []string               `json:"unresolved_files"`
	Stats            impactStats            `json:"stats"`
	Staleness        map[string]interface{} `json:"staleness"`
	StalenessWarning string                 `json:"staleness_warning,omitempty"`
	TokenBudget      int                    `json:"token_budget,omitempty"`
	TokensUsed       int                    `json:"tokens_used,omitempty"`
	Complete         *bool                  `json:"complete,omitempty"`
	Omitted          *impactOmitted         `json:"omitted,omitempty"`
	RecommendedNext  string                 `json:"recommended_next_action,omitempty"`
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
		if a.TokenBudget != nil && *a.TokenBudget <= 0 {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "token_budget must be positive"}
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

		impacted, truncated, maxDepth, err := queryImpact(store.DB(), seedIDs, depth, direction, includeTests, a.MinConfidence)
		if err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInternalError, Message: fmt.Sprintf("impact query: %v", err)}
		}
		rankImpactNodes(impacted)

		breakdown := map[string]int{}
		for _, n := range impacted {
			breakdown[n.ViaResolution]++
		}

		staleObj, staleWarn := stalenessInfo(store.DB())
		resp := impactResponse{
			Changed:         changed,
			Impacted:        impacted,
			UnresolvedFiles: unresolved,
			Stats: impactStats{
				ChangedFiles:        len(changed),
				ChangedSymbols:      len(seedSymbols),
				ImpactedSymbols:     len(impacted),
				MaxDepthReached:     maxDepth,
				Truncated:           truncated,
				ResolutionBreakdown: breakdown,
			},
			Staleness:        staleObj,
			StalenessWarning: staleWarn,
		}
		b, err := marshalImpactResponse(resp, impacted, truncated, a.TokenBudget)
		if err != nil {
			code := mcp.CodeInternalError
			if errors.Is(err, contextpack.ErrBudgetTooSmall) {
				code = mcp.CodeInvalidParams
			}
			return nil, &mcp.RPCError{Code: code, Message: err.Error()}
		}
		return []mcp.ContentItem{{Type: "text", Text: string(b)}}, nil
	}
}

func marshalImpactResponse(base impactResponse, impacted []impactedNode, graphTruncated bool, tokenBudget *int) ([]byte, error) {
	if tokenBudget == nil {
		base.Impacted = impacted
		return json.Marshal(base)
	}
	counter, err := contextpack.NewCounter()
	if err != nil {
		return nil, err
	}
	total := len(impacted)
	fit, err := contextpack.FitJSONPrefix(counter, total, *tokenBudget, func(keep, tokensUsed int) ([]byte, error) {
		resp := base
		resp.Impacted = make([]impactedNode, keep)
		copy(resp.Impacted, impacted[:keep])
		resp.Stats.ReturnedImpactSymbols = keep
		resp.Stats.Truncated = graphTruncated || keep < total
		resp.TokenBudget = *tokenBudget
		resp.TokensUsed = tokensUsed
		complete := !resp.Stats.Truncated
		resp.Complete = &complete
		resp.Omitted = &impactOmitted{
			Impacted:               total - keep,
			BeyondGraphSafetyLimit: graphTruncated,
		}
		if !complete {
			resp.RecommendedNext = actionNarrowScope
		}
		return json.Marshal(resp)
	})
	if err != nil {
		return nil, err
	}
	return fit.JSON, nil
}

// rankImpactNodes makes budget selection independent of recursive-CTE emission
// order. Nearest and strongest structural evidence wins; tests get the tie-break
// within an equal depth/confidence tier.
func rankImpactNodes(nodes []impactedNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		if a.Depth != b.Depth {
			return a.Depth < b.Depth
		}
		if resolutionRank[a.ViaResolution] != resolutionRank[b.ViaResolution] {
			return resolutionRank[a.ViaResolution] > resolutionRank[b.ViaResolution]
		}
		if isTestFile(a.File) != isTestFile(b.File) {
			return isTestFile(a.File)
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Symbol < b.Symbol
	})
}

func queryImpact(database *sql.DB, seedIDs map[int64]string, depth int, direction string, includeTests bool, minConfidence string) ([]impactedNode, bool, int, error) {
	if len(seedIDs) == 0 {
		return []impactedNode{}, false, 0, nil
	}

	ids := make([]interface{}, 0, len(seedIDs))
	placeholders := make([]string, 0, len(seedIDs))
	for id := range seedIDs {
		ids = append(ids, id)
		placeholders = append(placeholders, "?")
	}

	// edgeFilter restricts which edges the walk may traverse, by resolution
	// confidence. With no min_confidence, the default EXCLUDES the low-confidence
	// interface-dispatch fan-out (so default blast radius is unchanged); requesting
	// a low min_confidence (e.g. "interface-dispatch") opts them in, while a high
	// one excludes them along with other weak edges.
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

	collect := func(walkSQL string) ([]impactedNode, int, error) {
		query := fmt.Sprintf(`
			WITH RECURSIVE chain(id, name, file_path, line, depth, via_id, via_res) AS (
				SELECT f.id, f.name, f.file_path, f.start_line, 0, f.id, ''
				FROM functions f WHERE f.id IN (%s)
				UNION ALL
				%s
			)
			SELECT chain.id, chain.name, chain.file_path, chain.line, chain.depth,
			       seed.name AS via_name, chain.via_res
			FROM chain
			JOIN functions seed ON seed.id = chain.via_id
			WHERE chain.depth > 0
			ORDER BY chain.depth, chain.name
			LIMIT ?
		`, strings.Join(placeholders, ","), walkSQL)
		// Arg order: seed ids, then (depth + edge-filter markers) per walk
		// reference, then the LIMIT. The walk SQL references ? for depth first,
		// then the edge-filter placeholders.
		args := append([]interface{}{}, ids...)
		args = append(args, depth)
		args = append(args, filterArgs...)
		args = append(args, maxImpactNodes+1)
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
			if err := rows.Scan(&id, &n.Symbol, &n.File, &n.Line, &n.Depth, &via, &n.ViaResolution); err != nil {
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
		SELECT f.id, f.name, f.file_path, f.start_line, chain.depth + 1, chain.via_id, e.resolution
		FROM chain
		JOIN calls e ON e.callee_id = chain.id
		JOIN functions f ON f.id = e.caller_id
		WHERE chain.depth < ?` + edgeFilter + ` AND f.id != chain.id
	`
	calleesWalk := `
		SELECT f.id, f.name, f.file_path, f.start_line, chain.depth + 1, chain.via_id, e.resolution
		FROM chain
		JOIN calls e ON e.caller_id = chain.id
		JOIN functions f ON f.id = e.callee_id
		WHERE chain.depth < ?` + edgeFilter + ` AND f.id != chain.id
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
