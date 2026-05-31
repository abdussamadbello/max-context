package tools

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/mcp"
)

func TestQueryCodebase_SuggestionsOnZeroResults(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		t.Fatalf("PrepareQueries: %v", err)
	}
	defer q.Close()

	// Seed two distinctive names so a typo can still substring-match "auth".
	if _, err := q.InsertFunction.Exec("AuthenticateUser", "auth.go", 1, 10, "go", 1, "", "", "AuthenticateUser()"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.InsertFunction.Exec("AuthorizeRequest", "auth.go", 20, 30, "go", 1, "", "", "AuthorizeRequest()"); err != nil {
		t.Fatal(err)
	}
	if err := db.RebuildAllFTS(database); err != nil {
		t.Fatal(err)
	}

	h := QueryCodebaseHandler(database, q, "")
	resp, err := h(json.RawMessage(`{"query":"authnticate"}`)) // typo: missing 'e' before 'n'
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	items := resp.([]mcp.ContentItem)
	var out struct {
		Results     []map[string]interface{} `json:"results"`
		Suggestions []string                 `json:"suggestions,omitempty"`
	}
	if err := json.Unmarshal([]byte(items[0].Text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Results) != 0 {
		t.Fatalf("expected 0 results on typo, got %d", len(out.Results))
	}
	if len(out.Suggestions) == 0 {
		t.Fatalf("expected suggestions on weak result")
	}
}

// TestQueryCodebase_NudgeAfterRepeats verifies the server injects a switch-tools
// nudge once the model has queried several times (FINDINGS gap 1 backstop).
func TestQueryCodebase_NudgeAfterRepeats(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "n.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	q.InsertFunction.Exec("Foo", "a.go", 1, 5, "go", 1, "", "", "Foo")
	db.RebuildAllFTS(database)

	h := QueryCodebaseHandler(database, q, "")
	hasNudge := func() bool {
		resp, _ := h(json.RawMessage(`{"query":"something"}`))
		var out map[string]interface{}
		json.Unmarshal([]byte(resp.([]mcp.ContentItem)[0].Text), &out)
		_, ok := out["nudge"]
		return ok
	}
	// First few calls: no nudge.
	for i := 0; i < 3; i++ {
		if hasNudge() {
			t.Fatalf("unexpected nudge on call %d", i+1)
		}
	}
	// By the 4th+ call, the nudge appears.
	if !hasNudge() {
		t.Error("expected nudge after repeated queries")
	}
}

func TestQueryCodebase_RepeatedSearchDefinitiveHitStillAnswersNow(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "definitive-after-repeat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if _, err := q.InsertFunction.Exec("Foo", "a.go", 1, 5, "go", 1, "func Foo() {}", "", "Foo"); err != nil {
		t.Fatal(err)
	}
	if err := db.RebuildAllFTS(database); err != nil {
		t.Fatal(err)
	}

	h := QueryCodebaseHandler(database, q, "")
	for i := 0; i < 3; i++ {
		if _, err := h(json.RawMessage(`{"query":"missing"}`)); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := h(json.RawMessage(`{"query":"Foo"}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		AnswerStatus          string                 `json:"answer_status"`
		RecommendedNextAction string                 `json:"recommended_next_action"`
		LoopGuard             map[string]interface{} `json:"loop_guard"`
	}
	items := resp.([]mcp.ContentItem)
	if err := json.Unmarshal([]byte(items[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if out.AnswerStatus != "definitive" {
		t.Fatalf("expected definitive answer after exact hit, got %q", out.AnswerStatus)
	}
	if out.RecommendedNextAction != "answer_now" {
		t.Fatalf("expected answer_now after exact hit, got %q", out.RecommendedNextAction)
	}
	if out.LoopGuard == nil {
		t.Fatal("expected loop_guard metadata after repeated searches")
	}
}

func TestDetectedIntent(t *testing.T) {
	cases := map[string]string{
		"where is URL defined":           "definition",
		"what depends on Timeout":        "impact",
		"what breaks if I change Foo":    "impact",
		"add a default header":           "edit_target",
		"connection pool implementation": "search",
	}
	for query, want := range cases {
		if got := detectedIntent(query); got != want {
			t.Fatalf("detectedIntent(%q) = %q, want %q", query, got, want)
		}
	}
}

func TestQueryCodebase_OverloadedExactNamePrefersCanonicalType(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "overloaded.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	if _, err := database.Exec(`
		INSERT INTO functions (name, file_path, start_line, end_line, language, exported, code, docstring, signature, kind)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"Timeout", "httpx/_client.py", 254, 260, "python", 1, "def Timeout(self): pass", "", "Timeout", "method"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.InsertType.Exec("Timeout", "httpx/_config.py", "class", "class Timeout:", 1); err != nil {
		t.Fatal(err)
	}
	if err := db.RebuildAllFTS(database); err != nil {
		t.Fatal(err)
	}

	h := QueryCodebaseHandler(database, q, "")
	resp, err := h(json.RawMessage(`{"query":"Timeout","max_results":5}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Results               []map[string]interface{} `json:"results"`
		Canonical             map[string]interface{}   `json:"canonical"`
		SecondaryExactMatches []map[string]interface{} `json:"secondary_exact_matches"`
		AnswerStatus          string                   `json:"answer_status"`
		RecommendedNextAction string                   `json:"recommended_next_action"`
	}
	items := resp.([]mcp.ContentItem)
	if err := json.Unmarshal([]byte(items[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if out.AnswerStatus != "definitive" {
		t.Fatalf("expected definitive answer, got %q", out.AnswerStatus)
	}
	if out.RecommendedNextAction != "answer_now" {
		t.Fatalf("expected answer_now action, got %q", out.RecommendedNextAction)
	}
	if len(out.Results) != 1 {
		t.Fatalf("expected only canonical result to lead, got %d", len(out.Results))
	}
	if got := out.Canonical["file"]; got != "httpx/_config.py" {
		t.Fatalf("expected canonical type file, got %v", got)
	}
	if len(out.SecondaryExactMatches) != 1 {
		t.Fatalf("expected secondary method match, got %d", len(out.SecondaryExactMatches))
	}
}

// TestQueryCodebase_TransientFTSFailureIsBusy reproduces the E01 root cause: the
// FTS table is transiently unavailable (here: dropped, as during a background
// rebuild) while the base functions table still has rows. The handler must report
// CodeIndexBusy ("rebuilding, retry") — NOT CodeIndexNotReady ("run /index-codebase
// first"), which made the agent loop on the same failing query.
func TestQueryCodebase_TransientFTSFailureIsBusy(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		t.Fatalf("PrepareQueries: %v", err)
	}
	defer q.Close()

	// Base table has data...
	if _, err := q.InsertFunction.Exec("Handler", "h.go", 1, 10, "go", 1, "", "", "Handler()"); err != nil {
		t.Fatal(err)
	}
	// ...but the FTS index is transiently gone (simulating mid-rebuild).
	if _, err := database.Exec("DROP TABLE functions_fts"); err != nil {
		t.Fatal(err)
	}

	h := QueryCodebaseHandler(database, q, "")
	_, err = h(json.RawMessage(`{"query":"handler"}`))
	if err == nil {
		t.Fatal("expected an error when FTS is unavailable")
	}
	rpcErr, ok := err.(*mcp.RPCError)
	if !ok {
		t.Fatalf("expected *mcp.RPCError, got %T", err)
	}
	if rpcErr.Code != mcp.CodeIndexBusy {
		t.Errorf("FTS-down-but-data-present must be CodeIndexBusy (%d), got %d: %q",
			mcp.CodeIndexBusy, rpcErr.Code, rpcErr.Message)
	}
}

// TestQueryCodebase_GenuinelyEmptyIsNotReady confirms the other branch: no FTS AND
// no base-table rows -> CodeIndexNotReady (the honest "run /index-codebase first").
func TestQueryCodebase_GenuinelyEmptyIsNotReady(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		t.Fatalf("PrepareQueries: %v", err)
	}
	defer q.Close()

	// No functions inserted; drop FTS to force the query error path.
	if _, err := database.Exec("DROP TABLE functions_fts"); err != nil {
		t.Fatal(err)
	}
	h := QueryCodebaseHandler(database, q, "")
	_, err = h(json.RawMessage(`{"query":"anything"}`))
	rpcErr, ok := err.(*mcp.RPCError)
	if !ok {
		t.Fatalf("expected *mcp.RPCError, got %T (%v)", err, err)
	}
	if rpcErr.Code != mcp.CodeIndexNotReady {
		t.Errorf("empty index must be CodeIndexNotReady (%d), got %d", mcp.CodeIndexNotReady, rpcErr.Code)
	}
}
