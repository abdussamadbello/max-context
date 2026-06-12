package tools

import (
	"database/sql"
	"strings"

	"github.com/maxcontext/max-context/internal/mcp"
)

// ftsSearch runs a prepared FTS MATCH search with the agent-query fallback: it
// first tries the raw query (so power users keep full FTS5 syntax), and only on
// an FTS5 syntax error retries with the sanitized form. A query that sanitizes to
// nothing searchable returns (nil, nil) — an empty result set, not an error. Any
// non-syntax error (busy/lock) is returned unwrapped for the caller to classify.
func ftsSearch(stmt *sql.Stmt, rawQuery string, limit int) (*sql.Rows, error) {
	rows, err := stmt.Query(rawQuery, limit)
	if err == nil {
		return rows, nil
	}
	if !isFTSSyntaxError(err) {
		return nil, err
	}
	sanitized := sanitizeFTSQuery(rawQuery)
	if sanitized == "" {
		return nil, nil
	}
	return stmt.Query(sanitized, limit)
}

// ftsSearchPrebuilt runs an already-built FTS5 MATCH expression (from
// expandFTSQuery) with no sanitization fallback. Expansion is best-effort: a
// syntax error degrades to an empty result set, never an error.
func ftsSearchPrebuilt(stmt *sql.Stmt, query string, limit int) (*sql.Rows, error) {
	rows, err := stmt.Query(query, limit)
	if err != nil {
		if isFTSSyntaxError(err) {
			return nil, nil
		}
		return nil, err
	}
	return rows, nil
}

// splitIdentifier splits a compound identifier into lowercase word parts:
// camelCase ("getUserByID" -> get, user, by, id — acronym runs handled, so
// "HTTPServer" -> http, server), snake_case, and digit->letter boundaries.
// Parts shorter than 2 characters are dropped. Returns nil when the token
// doesn't usefully split (fewer than 2 parts).
func splitIdentifier(tok string) []string {
	runes := []rune(tok)
	var parts []string
	var cur []rune
	flush := func() {
		if len(cur) >= 2 {
			parts = append(parts, strings.ToLower(string(cur)))
		}
		cur = nil
	}
	isUpper := func(r rune) bool { return r >= 'A' && r <= 'Z' }
	isLower := func(r rune) bool { return r >= 'a' && r <= 'z' }
	isDigit := func(r rune) bool { return r >= '0' && r <= '9' }
	for i, r := range runes {
		if r == '_' {
			flush()
			continue
		}
		if i > 0 {
			prev := runes[i-1]
			switch {
			case isUpper(r) && (isLower(prev) || isDigit(prev)):
				// wordEnd|NewWord
				flush()
			case isUpper(prev) && isUpper(r) && i+1 < len(runes) && isLower(runes[i+1]):
				// acronym run ends: HTTP|Server
				flush()
			case isLower(r) && isDigit(prev):
				// digit run ends: html5|parser (letters after digits start a word;
				// digits stay attached to the word before them: "html5")
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	if len(parts) < 2 {
		return nil
	}
	return parts
}

// expandFTSQuery rewrites a query so compound identifiers also match their
// word parts: each splittable token becomes ("getUserByID" OR ("get" "user"
// "by" "id")); other tokens stay quoted; groups are ANDed. Returns "" when no
// token splits — there is nothing the plain sanitized query didn't already
// try. Used only as a zero-results fallback so default ranking stays exact.
func expandFTSQuery(q string) string {
	tokens := tokenizeFTS(q)
	if len(tokens) == 0 {
		return ""
	}
	anySplit := false
	var b strings.Builder
	for i, tok := range tokens {
		if i > 0 {
			b.WriteByte(' ')
		}
		parts := splitIdentifier(tok)
		if parts == nil {
			b.WriteString(`"` + tok + `"`)
			continue
		}
		anySplit = true
		b.WriteString(`("` + tok + `" OR (`)
		for j, p := range parts {
			if j > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(`"` + p + `"`)
		}
		b.WriteString(`))`)
	}
	if !anySplit {
		return ""
	}
	return b.String()
}

// classifyFTSError maps a search failure to the right RPC error. A syntax error
// (should be rare now that ftsSearch sanitizes) tells the agent to simplify the
// query — NOT CodeIndexBusy, which made it retry the same broken query in a loop.
// Otherwise it distinguishes a transient busy/lock (data present) from a
// genuinely-unbuilt index.
func classifyFTSError(database *sql.DB, err error) *mcp.RPCError {
	if isFTSSyntaxError(err) {
		return &mcp.RPCError{Code: mcp.CodeQuerySyntax, Message: "search query is not valid: use plain keywords (the server quotes terms for you) and avoid raw FTS5 operators like NEAR/AND/OR or unbalanced quotes"}
	}
	if indexHasFunctions(database) {
		return &mcp.RPCError{Code: mcp.CodeIndexBusy, Message: "index is rebuilding; retry this query shortly, or use get_definition/get_call_chain which are unaffected"}
	}
	return &mcp.RPCError{Code: mcp.CodeIndexNotReady, Message: "index not ready: run /index-codebase first"}
}

// sanitizeFTSQuery turns an arbitrary agent query into a safe FTS5 MATCH
// expression. Coding agents pass code fragments ("db.Open(path)"), hyphenated
// phrases ("retry-logic"), natural-language questions, and FTS keywords (NEAR,
// AND, OR) as bare words — none of which is valid FTS5 syntax, so passing them
// through raw raised syntax errors.
//
// Rule: split on every non-alphanumeric character (underscore kept, so
// identifiers like snake_case survive), drop empties, double-quote each token
// (neutralizing every FTS operator), join with spaces (implicit AND), and append
// '*' to the final token for prefix matching. Example:
//
//	db.Open(path  ->  "db" "Open" "path"*
//
// Returns "" when the query has no usable tokens; the caller treats that as an
// empty result set, never an error.
func sanitizeFTSQuery(q string) string {
	tokens := tokenizeFTS(q)
	if len(tokens) == 0 {
		return ""
	}
	var b strings.Builder
	for i, tok := range tokens {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('"')
		b.WriteString(tok)
		b.WriteByte('"')
		if i == len(tokens)-1 {
			b.WriteByte('*') // prefix-match the trailing token
		}
	}
	return b.String()
}

// tokenizeFTS splits on any character that is not a letter, digit, or underscore.
func tokenizeFTS(q string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range q {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// isFTSSyntaxError reports whether err is a malformed-MATCH error from FTS5 (as
// opposed to a transient lock/busy or an I/O failure). FTS5 reports a bad query
// several ways: "fts5: syntax error near X" for stray punctuation/operators, and
// "no such column: X" when a hyphen/colon makes it read a bare word as a column
// filter (e.g. "retry-logic"). All of these mean the agent sent a query that
// isn't valid FTS5 and should simplify it — never the "index is rebuilding;
// retry" signal, which makes the agent loop on the same bad query.
//
// Called only in the FTS-search path, where the prepared statement is fixed and
// the only variable is the MATCH argument, so these signatures unambiguously
// indicate a query problem rather than a schema problem.
func isFTSSyntaxError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// Transient/infrastructure failures are NOT query-syntax problems.
	if strings.Contains(msg, "locked") || strings.Contains(msg, "busy") ||
		strings.Contains(msg, "i/o") || strings.Contains(msg, "disk") {
		return false
	}
	return strings.Contains(msg, "fts5: syntax error") ||
		strings.Contains(msg, "no such column") ||
		strings.Contains(msg, "unterminated") ||
		strings.Contains(msg, "malformed")
}
