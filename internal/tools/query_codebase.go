package tools

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

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
	File      string `json:"file"`
	Line      int    `json:"line,omitempty"`
	Kind      string `json:"kind"`
	Label     string `json:"label,omitempty"`
	Name      string `json:"name"`
	Snippet   string `json:"snippet,omitempty"`
	Canonical bool   `json:"canonical,omitempty"`
}

// maxSnippetLen caps the per-result snippet so responses stay terse — the
// experiment (experiments/eval/FINDINGS.md) showed verbose responses both cost
// tokens and encouraged the model to keep querying instead of answering.
const maxSnippetLen = 160

func QueryCodebaseHandler(database *sql.DB, q *db.Queries, projectRoot string) mcp.ToolHandler {
	// Per-session query counter: when the model issues many query_codebase calls,
	// it is usually looping on a diffuse "what-depends-on-X" task that get_impact
	// answers in one call (FINDINGS gap 1). After a threshold we inject a nudge to
	// switch tools / commit — a server-side backstop that works even when the
	// model ignores the prompt guidance.
	callCount := 0
	const nudgeAfter = 4

	return func(args json.RawMessage) (interface{}, error) {
		callCount++
		var a queryCodebaseArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
			}
		}
		if a.Query == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "query required"}
		}
		limit := 3
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
		switch scope {
		case "all", "functions", "types", "files", "docs":
		default:
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "scope must be one of: all, functions, types, files, docs"}
		}
		fileFilter := ""
		var fileFilterRE *regexp.Regexp
		if a.FileFilter != nil {
			fileFilter = strings.TrimSpace(*a.FileFilter)
			compiled, compileErr := compilePathGlob(fileFilter)
			if compileErr != nil {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "invalid file_filter: " + compileErr.Error()}
			}
			fileFilterRE = compiled
		}

		// Over-fetch from FTS so the exact-name promotion below has a pool to
		// find the right symbol in. bm25 ranks a long test name matching every
		// query term above the short symbol the agent actually wants, so with a
		// default limit of 3 the real answer never entered the candidate set.
		// Results are trimmed back to `limit` before returning.
		fetchLimit := limit * 8
		if fetchLimit < 30 {
			fetchLimit = 30
		}
		candidateLimit := fetchLimit
		// Filtering happens after FTS ranking because SQLite's prepared MATCH
		// statements are deliberately shared and compact. With a path filter, let
		// SQLite stream the complete ranked match set and stop after enough
		// in-filter candidates; limiting before filtering makes narrow filters lie.
		if fileFilter != "" {
			fetchLimit = -1
			if candidateLimit < 500 {
				candidateLimit = 500
			}
		}

		// search runs all in-scope FTS lookups for one query string; prebuilt
		// queries (the expansion fallback) skip the raw-then-sanitize logic.
		search := func(query string, prebuilt bool) ([]searchResult, error) {
			fts := ftsSearch
			if prebuilt {
				fts = ftsSearchPrebuilt
			}
			var out []searchResult
			if scope == "all" || scope == "functions" {
				matched := 0
				rows, err := fts(q.SearchFunctions, query, fetchLimit)
				if err != nil {
					return out, err
				}
				for rows != nil && rows.Next() {
					var id int64
					var name, filePath string
					var startLine, endLine int
					var language string
					var fnKind sql.NullString
					var exported int
					var code, docstring, signature, snippet sql.NullString
					var rank float64
					if rows.Scan(&id, &name, &filePath, &startLine, &endLine, &language, &fnKind, &exported, &code, &docstring, &signature, &snippet, &rank) == nil {
						kind := "function"
						if fnKind.Valid && fnKind.String == "method" {
							kind = "method"
						}
						r := searchResult{
							File: filePath, Line: startLine, Kind: kind, Name: name, Snippet: truncateSnippet(snippet.String),
						}
						annotateSearchResult(&r)
						if pathMatchesCompiled(r.File, fileFilterRE) {
							out = append(out, r)
							matched++
							if matched == candidateLimit {
								break
							}
						}
					}
				}
				if rows != nil {
					rows.Close()
				}
			}
			if scope == "all" || scope == "types" {
				matched := 0
				// Types search is best-effort: a failure here never fails the whole
				// call (functions results may already be present). ftsSearch applies the
				// same raw-then-sanitized fallback.
				rows, err := fts(q.SearchTypes, query, fetchLimit)
				if err == nil && rows != nil {
					for rows.Next() {
						var id int64
						var name, filePath, kind string
						var startLine int
						var definition string
						var exported int
						var snippet sql.NullString
						var rank float64
						if rows.Scan(&id, &name, &filePath, &startLine, &kind, &definition, &exported, &snippet, &rank) == nil {
							r := searchResult{
								File: filePath, Line: startLine, Kind: "type/" + kind, Name: name, Snippet: truncateSnippet(snippet.String),
							}
							annotateSearchResult(&r)
							if pathMatchesCompiled(r.File, fileFilterRE) {
								out = append(out, r)
								matched++
								if matched == candidateLimit {
									break
								}
							}
						}
					}
					rows.Close()
				}
			}
			if scope == "all" || scope == "docs" {
				// Document search is best-effort, like types. In "all", docs are appended
				// AFTER code results and capped low: bm25 ranks are not comparable across
				// FTS tables, and code must stay the primary answer surface.
				docLimit := limit
				if scope == "all" && docLimit > 3 {
					docLimit = 3
				}
				docCandidateLimit := docLimit
				if fileFilter != "" {
					docLimit = -1
				}
				rows, err := fts(q.SearchDocuments, query, docLimit)
				if err == nil && rows != nil {
					for rows.Next() {
						var id int64
						var filePath string
						var title, kind, snippet sql.NullString
						var rank float64
						if rows.Scan(&id, &filePath, &title, &kind, &snippet, &rank) == nil {
							name := title.String
							if name == "" {
								name = filepath.Base(filePath)
							}
							if pathMatchesCompiled(filePath, fileFilterRE) {
								out = append(out, searchResult{
									File: filePath, Kind: "document", Name: name, Snippet: truncateSnippet(snippet.String),
								})
								if docCandidateLimit > 0 {
									docCandidateLimit--
									if docCandidateLimit == 0 {
										break
									}
								}
							}
						}
					}
					rows.Close()
				}
			}
			return out, nil
		}

		var results []searchResult
		var err error
		if scope == "files" {
			results, err = searchFiles(database, a.Query, fileFilterRE, limit)
		} else {
			results, err = search(a.Query, false)
		}
		if err != nil {
			return nil, classifyFTSError(database, err)
		}

		// Identifier-splitting fallback: a query like "getUserByID handler" that
		// AND-matched nothing retries with compound tokens expanded to their word
		// parts. Fallback-only, so default ranking stays exact-match.
		expandedQuery := false
		if len(results) == 0 && scope != "files" {
			if exp := expandFTSQuery(a.Query); exp != "" {
				if expResults, expErr := search(exp, true); expErr == nil && len(expResults) > 0 {
					results = expResults
					expandedQuery = true
				}
			}
		}

		// Constrained query expansion: when no results came back, surface candidate
		// names from the index that substring-match the user's query terms. The LLM
		// can re-issue a query with one of these to land on a real symbol.
		var suggestions []string
		if len(results) == 0 {
			if scope == "files" {
				suggestions = nearbyFiles(database, a.Query, fileFilterRE)
			} else {
				suggestions = nearbyTerms(database, a.Query, fileFilterRE)
			}
		}

		// Decisive stop-signal: when exactly one result's name matches the query
		// term exactly (case-insensitive), lead with THAT result alone and tell
		// the model it's the definitive hit so it (a) stops querying and (b)
		// reports the single correct file instead of also naming the other ranked
		// candidates — which was dropping precision (FINDINGS.md gap 2: P=0.50).
		exact := exactNameMatches(a.Query, results)
		// The over-fetched pool has served its purpose; the caller asked for
		// `limit` results and pays tokens for every one of them.
		if len(results) > limit {
			results = results[:limit]
		}
		payload := map[string]interface{}{
			"detected_intent": detectedIntent(a.Query),
		}
		if len(exact) == 1 {
			exact[0].Canonical = true
			payload["results"] = exact
			payload["total"] = 1
			payload["canonical"] = exact[0]
			payload["answer_status"] = answerStatusDefinitive
			payload["recommended_next_action"] = actionAnswerNow
			payload["ambiguous"] = false
			payload["answer"] = answerFor(exact[0]) +
				" (Definitive — answer with this file; other lower-ranked matches omitted.)"
		} else if len(exact) > 1 {
			if canonical, secondary, ok := canonicalSearchResult(exact); ok {
				canonical.Canonical = true
				payload["results"] = []searchResult{canonical}
				payload["total"] = 1
				payload["canonical"] = canonical
				payload["secondary_exact_matches"] = secondary
				payload["answer_status"] = answerStatusDefinitive
				payload["recommended_next_action"] = actionAnswerNow
				payload["ambiguous"] = false
				payload["overloaded"] = true
				payload["disambiguation_reason"] = "Multiple exact matches found; canonical type/class definitions outrank same-named methods/properties for definition queries."
				payload["answer"] = answerFor(canonical) +
					" Same-named secondary matches are included for context; answer with the canonical file."
			} else {
				payload["results"] = exact
				payload["total"] = len(exact)
				payload["answer_status"] = answerStatusAmbiguous
				payload["recommended_next_action"] = actionInspectRelevantFile
				payload["ambiguous"] = true
				payload["ambiguity_reason"] = "Multiple exact matches have equal priority; choose the match in the relevant module."
			}
		} else {
			payload["results"] = results
			payload["total"] = len(results)
			payload["answer_status"] = answerStatusPartial
			if len(results) == 0 {
				payload["recommended_next_action"] = actionCallQueryCodebase
			} else {
				payload["recommended_next_action"] = actionInspectTopResult
				if payload["detected_intent"] == "impact" {
					payload["recommended_next_action"] = actionCallImpactOrChain
				}
			}
		}
		if expandedQuery {
			// Observable signal that results came from the identifier-splitting
			// retry, not an exact match of the original query.
			payload["expanded_query"] = true
		}
		if len(suggestions) > 0 {
			payload["suggestions"] = suggestions
			payload["recommended_next_action"] = actionRetryWithSuggestion
		}
		if callCount >= nudgeAfter {
			payload["loop_guard"] = map[string]interface{}{
				"query_codebase_calls": callCount,
				"level":                loopGuardLevel(callCount),
			}
			if payload["answer_status"] == answerStatusDefinitive {
				payload["nudge"] = "You have searched several times this session, but this response contains a definitive exact match. Answer now with the canonical result."
			} else {
				payload["answer_status"] = answerStatusPartial
				payload["recommended_next_action"] = actionSwitchToolsOrAnswer
				payload["nudge"] = "You have searched several times this session. If you are " +
					"tracing what depends on a symbol, call get_impact or get_call_chain instead of " +
					"more keyword searches — they answer 'what uses/breaks this?' directly. Otherwise, " +
					"commit to an answer with what you have."
			}
		}
		attachStaleness(payload, database)
		b, _ := json.Marshal(payload)
		return []mcp.ContentItem{{Type: "text", Text: string(b)}}, nil
	}
}

// truncateSnippet caps a snippet to keep responses terse.
func truncateSnippet(s string) string {
	if len(s) <= maxSnippetLen {
		return s
	}
	return s[:maxSnippetLen] + "…"
}

// exactNameMatches returns results whose name equals a query token exactly
// (case-insensitive). When exactly one matches, the caller leads with it and
// drops the other ranked candidates (precision fix, FINDINGS gap 2).
func exactNameMatches(query string, results []searchResult) []searchResult {
	terms := splitTerms(query)
	// A multi-word query often spells one identifier: "resolver cache" is how an
	// agent asks for ResolverCache, and "index file" for IndexFile. Joining the
	// terms recovers the symbol, so those queries get the same decisive
	// treatment as typing the name directly.
	joined := strings.Join(terms, "")
	var exact []searchResult
	for _, r := range results {
		// A document titled exactly like the query must never trigger the
		// "definitive, stop searching" path reserved for symbol definitions.
		if r.Kind == "document" || r.Kind == "file" {
			continue
		}
		if len(terms) > 1 && equalFold(alphanumeric(r.Name), joined) {
			exact = append(exact, r)
			continue
		}
		for _, t := range terms {
			if equalFold(r.Name, t) {
				exact = append(exact, r)
				break
			}
		}
	}
	return exact
}

// alphanumeric strips separators from a symbol name so snake_case and camelCase
// spellings of the same identifier compare equal.
func alphanumeric(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// answerFor renders the decisive one-line location for a single result.
func answerFor(r searchResult) string {
	loc := formatLocation(r.File, r.Line)
	return "Exact match: " + r.Name + " (" + r.Kind + ") is defined at " + loc +
		". This is the definitive location — you can answer now without further searches."
}

func loopGuardLevel(callCount int) string {
	if callCount >= 6 {
		return "stop_searching"
	}
	return "warning"
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// indexHasFunctions reports whether the base functions table holds any rows. Used
// to distinguish a transient FTS failure (data present, table rebuilding/locked)
// from a genuinely-unbuilt index, so query_codebase returns an honest error rather
// than a misleading "run /index-codebase first" that makes the agent loop.
func indexHasFunctions(database *sql.DB) bool {
	var n int
	// LIMIT 1 existence check; querying the base table doesn't touch the FTS index.
	if err := database.QueryRow("SELECT EXISTS(SELECT 1 FROM functions LIMIT 1)").Scan(&n); err != nil {
		return false // base table itself unavailable -> treat as not ready
	}
	return n == 1
}

// nearbyTerms returns up to 5 indexed symbol names whose name contains any token
// of length >= 3 from the user query. Cheap, no FTS dependency, degrades to empty.
func nearbyTerms(database *sql.DB, query string, fileFilter *regexp.Regexp) []string {
	var terms []string
	for _, t := range splitTerms(query) {
		if len(t) >= 3 {
			terms = append(terms, t)
		}
	}
	if len(terms) == 0 {
		return nil
	}
	out := []string{}
	seen := map[string]bool{}
	for _, t := range terms {
		// Try all prefix substrings of the term down to length 3.
		// This lets a typo like "authnticate" still match "AuthenticateUser"
		// via the shared prefix "auth".
		for prefixLen := len(t); prefixLen >= 3; prefixLen-- {
			like := "%" + t[:prefixLen] + "%"
			for _, table := range []string{"functions", "types"} {
				rows, err := database.Query("SELECT DISTINCT name, file_path FROM "+table+" WHERE name LIKE ? COLLATE NOCASE LIMIT 50", like)
				if err != nil {
					continue
				}
				for rows.Next() {
					var name, file string
					if rows.Scan(&name, &file) == nil && pathMatchesCompiled(file, fileFilter) && !seen[name] {
						seen[name] = true
						out = append(out, name)
						if len(out) >= 5 {
							rows.Close()
							return out
						}
					}
				}
				rows.Close()
			}
			if len(out) >= 5 {
				return out
			}
		}
	}
	return out
}

func splitTerms(query string) []string {
	var out []string
	cur := ""
	for _, r := range query {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			cur += string(r)
			continue
		}
		if cur != "" {
			out = append(out, cur)
			cur = ""
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
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
