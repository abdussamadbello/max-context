package tools

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/mcp"
)

// agentStyleQueries are the kinds of strings coding agents actually pass to
// query_codebase: code fragments, hyphenated phrases, natural-language questions
// with punctuation, and FTS keywords used as bare words. Each is NOT valid FTS5
// MATCH syntax, so before sanitization they raised fts5 syntax errors that were
// then misreported as CodeIndexBusy ("rebuilding; retry") — driving the agent to
// retry the same broken query in a loop (the E01 7-turn over-exploration).
var agentStyleQueries = []string{
	"db.Open(path)",
	"retry-logic",
	`what calls "Stop"?`,
	"foo NEAR bar",
}

func newSearchDB(t *testing.T) (*sql.DB, *db.Queries) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "fts.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		t.Fatalf("PrepareQueries: %v", err)
	}
	// Seed a couple of symbols so the FTS index is non-empty and a genuinely-unbuilt
	// index is not what we're testing.
	if _, err := q.InsertFunction.Exec("Open", "db.go", 1, 10, "go", 1, "open the database connection", "", "Open(path string)"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.InsertFunction.Exec("Stop", "engine.go", 1, 10, "go", 1, "stop the engine with retry logic", "", "Stop()"); err != nil {
		t.Fatal(err)
	}
	if err := db.RebuildAllFTS(database); err != nil {
		t.Fatal(err)
	}
	return database, q
}

// TestQueryCodebaseAgentQueriesNeverError verifies agent-style queries return
// results or an empty set — never an RPC error. (Pre-fix this failed: each query
// produced a CodeIndexBusy error.)
func TestQueryCodebaseAgentQueriesNeverError(t *testing.T) {
	database, q := newSearchDB(t)
	defer database.Close()
	defer q.Close()
	h := QueryCodebaseHandler(database, q, "")

	for _, query := range agentStyleQueries {
		args, _ := json.Marshal(map[string]string{"query": query})
		resp, err := h(json.RawMessage(args))
		if err != nil {
			t.Errorf("query %q returned error %v, want results-or-empty (no error)", query, err)
			continue
		}
		// Sanity: it is a well-formed content payload.
		if _, ok := resp.([]mcp.ContentItem); !ok {
			t.Errorf("query %q: unexpected response type %T", query, resp)
		}
	}
}

// TestSanitizeFTSQuery checks the tokenization + quoting + prefix rule.
func TestSanitizeFTSQuery(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"db.Open(path", `"db" "Open" "path"*`},
		{"retry-logic", `"retry" "logic"*`},
		{"foo NEAR bar", `"foo" "NEAR" "bar"*`},
		{"Stop", `"Stop"*`},
		{"", ""},
		{"!@#$", ""},
		{"snake_case_name", `"snake_case_name"*`},
	}
	for _, c := range cases {
		if got := sanitizeFTSQuery(c.in); got != c.want {
			t.Errorf("sanitizeFTSQuery(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestIsFTSSyntaxError distinguishes FTS5 syntax errors from busy/lock errors.
func TestIsFTSSyntaxError(t *testing.T) {
	if !isFTSSyntaxError(fmt.Errorf("fts5: syntax error near \".\"")) {
		t.Error("fts5 syntax error not classified as syntax")
	}
	if !isFTSSyntaxError(fmt.Errorf("SQL logic error: no such column: logic (1)")) {
		t.Error("fts5 'no such column' (hyphen misparse) not classified as syntax")
	}
	if isFTSSyntaxError(fmt.Errorf("database is locked (5) (SQLITE_BUSY)")) {
		t.Error("busy error misclassified as syntax")
	}
	if isFTSSyntaxError(nil) {
		t.Error("nil misclassified as syntax error")
	}
}

// TestClassifyFTSError verifies a syntax error maps to CodeQuerySyntax (simplify
// the query) and never to CodeIndexBusy (which triggered the retry loop), while a
// busy error with data present still maps to CodeIndexBusy.
func TestClassifyFTSError(t *testing.T) {
	if e := classifyFTSError(nil, fmt.Errorf(`fts5: syntax error near "?"`)); e.Code != mcp.CodeQuerySyntax {
		t.Errorf("syntax error mapped to %d, want CodeQuerySyntax %d", e.Code, mcp.CodeQuerySyntax)
	}
	database, q := newSearchDB(t)
	defer database.Close()
	defer q.Close()
	if e := classifyFTSError(database, fmt.Errorf("database is locked (5)")); e.Code != mcp.CodeIndexBusy {
		t.Errorf("busy error (data present) mapped to %d, want CodeIndexBusy %d", e.Code, mcp.CodeIndexBusy)
	}
}

func TestSplitIdentifier(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"getUserByID", []string{"get", "user", "by", "id"}},
		{"snake_case_name", []string{"snake", "case", "name"}},
		{"HTTPServer", []string{"http", "server"}},
		{"parseHTML5Doc", []string{"parse", "html5", "doc"}},
		{"plain", nil}, // single word: no useful split
		{"ID", nil},    // too short to split
		{"aB", nil},    // parts under 2 chars dropped -> fewer than 2 parts
	}
	for _, c := range cases {
		got := splitIdentifier(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitIdentifier(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitIdentifier(%q) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

func TestExpandFTSQuery(t *testing.T) {
	if got := expandFTSQuery("getUserByID"); got != `("getUserByID" OR ("get" "user" "by" "id"))` {
		t.Errorf("expandFTSQuery(getUserByID) = %s", got)
	}
	if got := expandFTSQuery("getUserByID handler"); got != `("getUserByID" OR ("get" "user" "by" "id")) "handler"` {
		t.Errorf("expandFTSQuery(two tokens) = %s", got)
	}
	// Nothing splits -> "" (the plain sanitized query already tried this).
	if got := expandFTSQuery("plain words only"); got != "" {
		t.Errorf("expandFTSQuery(plain) = %q, want empty", got)
	}
	if got := expandFTSQuery("!!!"); got != "" {
		t.Errorf("expandFTSQuery(no tokens) = %q, want empty", got)
	}
}
