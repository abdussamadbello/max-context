package tools

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/mcp"
)

type queryResponse struct {
	Results []struct {
		Name string `json:"name"`
		File string `json:"file"`
		Line int    `json:"line"`
		Kind string `json:"kind"`
	} `json:"results"`
	AnswerStatus string `json:"answer_status"`
	Total        int    `json:"total"`
}

func newQueryFixture(t *testing.T) (*sql.DB, *db.Queries, mcp.ToolHandler, func()) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
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
	h := QueryCodebaseHandler(database, q, "")
	return database, q, h, func() { q.Close(); database.Close() }
}

func runQuery(t *testing.T, h mcp.ToolHandler, query string) queryResponse {
	t.Helper()
	resp, err := h(json.RawMessage(`{"query":` + jsonString(query) + `}`))
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	items := resp.([]mcp.ContentItem)
	var out queryResponse
	if err := json.Unmarshal([]byte(items[0].Text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// The regression: FTS5 indexes "ResolverCache" as one token, so "resolver
// cache" could not match the symbol — but it did match the indexed file_path
// of every symbol in resolver_cache_test.go, which then outranked it. A test
// helper called joinLines was the top hit for "resolver cache".
func TestMultiWordQueryFindsCamelCaseSymbol(t *testing.T) {
	_, q, h, cleanup := newQueryFixture(t)
	defer cleanup()

	// The symbol we want, in a file whose path shares no query term.
	if _, err := q.InsertFunction.Exec("ResolverCache", "internal/indexer/resolver.go", 120, 140, "go", 1, "", "", "ResolverCache()"); err != nil {
		t.Fatal(err)
	}
	// Decoys living in a file whose PATH contains both query terms.
	for _, name := range []string{"joinLines", "callGraphSnapshot", "writeFile"} {
		if _, err := q.InsertFunction.Exec(name, "internal/indexer/resolver_cache_test.go", 10, 20, "go", 0, "", "", name+"()"); err != nil {
			t.Fatal(err)
		}
	}
	out := runQuery(t, h, "resolver cache")
	if len(out.Results) == 0 {
		t.Fatal("no results for 'resolver cache'")
	}
	if out.Results[0].Name != "ResolverCache" {
		t.Errorf("top result = %q (%s), want ResolverCache",
			out.Results[0].Name, out.Results[0].File)
	}
	if out.AnswerStatus != answerStatusDefinitive {
		t.Errorf("answer_status = %q, want %q — a query that spells one symbol is definitive",
			out.AnswerStatus, answerStatusDefinitive)
	}
}

func TestMultiWordQueryMatchesSnakeCase(t *testing.T) {
	_, q, h, cleanup := newQueryFixture(t)
	defer cleanup()

	if _, err := q.InsertFunction.Exec("remove_file", "store/files.go", 5, 9, "go", 0, "", "", "remove_file()"); err != nil {
		t.Fatal(err)
	}
	out := runQuery(t, h, "remove file")
	if len(out.Results) == 0 || out.Results[0].Name != "remove_file" {
		t.Fatalf("want remove_file first, got %+v", out.Results)
	}
}

// The exact-name promotion must reach a symbol that bm25 ranked outside the
// requested result count, which is why the FTS pool is over-fetched.
func TestExactSymbolBeatsLongerNamesRankedAbove(t *testing.T) {
	_, q, h, cleanup := newQueryFixture(t)
	defer cleanup()

	if _, err := q.InsertFunction.Exec("IndexFile", "internal/indexer/indexer.go", 496, 560, "go", 1, "", "", "IndexFile()"); err != nil {
		t.Fatal(err)
	}
	// Names that contain both query words more times than the target does.
	for _, name := range []string{
		"TestIndexFileReindexesCalleeFile",
		"TestIndexFileHandlesDeletedFile",
		"TestIndexFileRewritesFileIndex",
		"resolverForIndexFile",
		"IndexDocFile",
		"reindexFileList",
	} {
		if _, err := q.InsertFunction.Exec(name, "internal/indexer/incremental_test.go", 10, 20, "go", 0, "", "", name+"()"); err != nil {
			t.Fatal(err)
		}
	}

	out := runQuery(t, h, "index file")
	if len(out.Results) == 0 || out.Results[0].Name != "IndexFile" {
		t.Fatalf("want IndexFile first, got %+v", out.Results)
	}
}

// Over-fetching must not leak into the response: the caller pays tokens per
// result and asked for a specific number.
func TestOverFetchIsTrimmedToRequestedLimit(t *testing.T) {
	_, q, h, cleanup := newQueryFixture(t)
	defer cleanup()

	for _, name := range []string{
		"handleAlpha", "handleBeta", "handleGamma", "handleDelta",
		"handleEpsilon", "handleZeta", "handleEta", "handleTheta",
	} {
		if _, err := q.InsertFunction.Exec(name, "server/handlers.go", 1, 5, "go", 0, "", "", name+"()"); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := h(json.RawMessage(`{"query":"handle","max_results":2}`))
	if err != nil {
		t.Fatal(err)
	}
	var out queryResponse
	if err := json.Unmarshal([]byte(resp.([]mcp.ContentItem)[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) > 2 {
		t.Errorf("max_results=2 returned %d results", len(out.Results))
	}
}

// types had no start_line column at all, so every type result came back with a
// file and no line for the agent to jump to.
func TestTypeResultsCarryALine(t *testing.T) {
	database, _, h, cleanup := newQueryFixture(t)
	defer cleanup()

	// The indexer writes start_line directly; seed it the same way.
	if _, err := database.Exec(
		"INSERT INTO types (name, name_parts, file_path, start_line, kind, definition, exported) VALUES (?1, split_identifier(?1), ?2, ?3, ?4, ?5, ?6)",
		"PaymentProcessor", "billing/processor.go", 42, "class", "class PaymentProcessor:", 1,
	); err != nil {
		t.Fatal(err)
	}

	out := runQuery(t, h, "PaymentProcessor")
	if len(out.Results) == 0 {
		t.Fatal("no results")
	}
	r := out.Results[0]
	if r.Name != "PaymentProcessor" {
		t.Fatalf("got %q", r.Name)
	}
	if r.Line != 42 {
		t.Errorf("line = %d, want 42 — type results must carry a line to jump to", r.Line)
	}
}

// A multi-word query must also reach a type through the split name.
func TestMultiWordQueryFindsType(t *testing.T) {
	database, _, h, cleanup := newQueryFixture(t)
	defer cleanup()

	if _, err := database.Exec(
		"INSERT INTO types (name, name_parts, file_path, start_line, kind, definition, exported) VALUES (?1, split_identifier(?1), ?2, ?3, ?4, ?5, ?6)",
		"PaymentProcessor", "billing/processor.go", 42, "class", "class PaymentProcessor:", 1,
	); err != nil {
		t.Fatal(err)
	}

	out := runQuery(t, h, "payment processor")
	if len(out.Results) == 0 || out.Results[0].Name != "PaymentProcessor" {
		t.Fatalf("want PaymentProcessor for 'payment processor', got %+v", out.Results)
	}
	if out.Results[0].Line != 42 {
		t.Errorf("line = %d, want 42", out.Results[0].Line)
	}
}
