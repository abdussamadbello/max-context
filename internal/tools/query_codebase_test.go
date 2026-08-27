package tools

import (
	"encoding/json"
	"path/filepath"
	"strings"
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

// TestQueryCodebase_DocsScope verifies document search: scope "docs" returns
// document hits with snippets, "all" appends docs after code results, and a
// document title matching the query exactly never claims the definitive
// stop-searching path.
func TestQueryCodebase_DocsScope(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "docs.db"))
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

	if _, err := database.Exec(
		`INSERT INTO documents (file_path, title, kind, content) VALUES (?,?,?,?)`,
		"docs/deploy.md", "Deployment", "markdown", "Set the replica count in deploy.yaml before rollout."); err != nil {
		t.Fatal(err)
	}
	if _, err := q.InsertFunction.Exec("RolloutStatus", "deploy.go", 1, 10, "go", 1, "", "reports rollout progress", "RolloutStatus()"); err != nil {
		t.Fatal(err)
	}
	if err := db.RebuildAllFTS(database); err != nil {
		t.Fatal(err)
	}

	h := QueryCodebaseHandler(database, q, "")

	// scope "docs": only the document comes back.
	resp, err := h(json.RawMessage(`{"query":"replica count","scope":"docs"}`))
	if err != nil {
		t.Fatalf("docs scope: %v", err)
	}
	var out struct {
		Results []struct {
			File    string `json:"file"`
			Kind    string `json:"kind"`
			Name    string `json:"name"`
			Snippet string `json:"snippet"`
		} `json:"results"`
		AnswerStatus string `json:"answer_status"`
	}
	if err := json.Unmarshal([]byte(resp.([]mcp.ContentItem)[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || out.Results[0].Kind != "document" || out.Results[0].File != "docs/deploy.md" {
		t.Fatalf("docs scope results = %+v", out.Results)
	}
	if out.Results[0].Name != "Deployment" {
		t.Errorf("doc name = %q, want title Deployment", out.Results[0].Name)
	}
	if out.Results[0].Snippet == "" {
		t.Error("doc result should carry a snippet")
	}

	// scope "all": code first, then docs.
	resp, err = h(json.RawMessage(`{"query":"rollout","max_results":10}`))
	if err != nil {
		t.Fatalf("all scope: %v", err)
	}
	if err := json.Unmarshal([]byte(resp.([]mcp.ContentItem)[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("all scope results = %+v, want function + document", out.Results)
	}
	if out.Results[0].Kind == "document" || out.Results[1].Kind != "document" {
		t.Errorf("docs must rank after code in 'all': %+v", out.Results)
	}

	// A document titled exactly like the query is never "definitive".
	if _, err := database.Exec(
		`INSERT INTO documents (file_path, title, kind, content) VALUES (?,?,?,?)`,
		"docs/refs.md", "Rollout", "markdown", "Rollout details."); err != nil {
		t.Fatal(err)
	}
	resp, err = h(json.RawMessage(`{"query":"Rollout","scope":"docs"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(resp.([]mcp.ContentItem)[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if out.AnswerStatus == "definitive" {
		t.Error("document title match must not produce a definitive answer_status")
	}
}

// TestQueryCodebase_ExpansionFallback: a compound-identifier query that
// AND-matches nothing must retry with the identifier split into word parts
// and flag the response with expanded_query.
func TestQueryCodebase_ExpansionFallback(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "exp.db"))
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

	// Docstring holds the split words, never the compound token, so the raw
	// query "fetchUserProfile" matches nothing but its expansion does.
	if _, err := q.InsertFunction.Exec("LoadAccount", "account.go", 1, 10, "go", 1, "", "fetch user profile from the store", "LoadAccount()"); err != nil {
		t.Fatal(err)
	}
	if err := db.RebuildAllFTS(database); err != nil {
		t.Fatal(err)
	}

	h := QueryCodebaseHandler(database, q, "")
	resp, err := h(json.RawMessage(`{"query":"fetchUserProfile"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var out struct {
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
		ExpandedQuery bool `json:"expanded_query"`
	}
	if err := json.Unmarshal([]byte(resp.([]mcp.ContentItem)[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || out.Results[0].Name != "LoadAccount" {
		t.Fatalf("expansion fallback results = %+v", out.Results)
	}
	if !out.ExpandedQuery {
		t.Error("expanded_query flag missing")
	}
}

func TestQueryCodebase_FileFilterAndFilesScope(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "files.db"))
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

	for _, file := range []string{"internal/db/schema.go", "internal/http/schema.go", "cmd/schema.go"} {
		if _, err := q.InsertFunction.Exec("Migrate", file, 1, 5, "go", 1, "func Migrate() {}", "", "Migrate()"); err != nil {
			t.Fatal(err)
		}
	}
	// File search uses the index inventory, so even a source file with no
	// functions, types, or imports remains discoverable.
	if _, err := q.InsertFileSummary.Exec("internal/empty/schema_notes.go", "go", 0, 0, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.RebuildAllFTS(database); err != nil {
		t.Fatal(err)
	}
	h := QueryCodebaseHandler(database, q, "")

	resp, err := h(json.RawMessage(`{"query":"Migrate","file_filter":"internal/db/**"}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Results     []searchResult `json:"results"`
		Suggestions []string       `json:"suggestions"`
	}
	if err := json.Unmarshal([]byte(resp.([]mcp.ContentItem)[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || out.Results[0].File != "internal/db/schema.go" {
		t.Fatalf("filtered results = %+v", out.Results)
	}

	resp, err = h(json.RawMessage(`{"query":"Migrate","file_filter":"does-not-exist/**"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(resp.([]mcp.ContentItem)[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 0 || len(out.Suggestions) != 0 {
		t.Fatalf("out-of-filter result metadata = %+v", out)
	}

	resp, err = h(json.RawMessage(`{"query":"schema","scope":"files","file_filter":"internal/**/*.go","max_results":10}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(resp.([]mcp.ContentItem)[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 3 {
		t.Fatalf("file-scope results = %+v, want three internal files", out.Results)
	}
	for _, result := range out.Results {
		if result.Kind != "file" || !strings.HasPrefix(result.File, "internal/") {
			t.Errorf("unexpected file result: %+v", result)
		}
	}

	if _, err := h(json.RawMessage(`{"query":"Migrate","scope":"made-up"}`)); err == nil {
		t.Fatal("invalid scope should return invalid params")
	} else if rpcErr, ok := err.(*mcp.RPCError); !ok || rpcErr.Code != mcp.CodeInvalidParams {
		t.Fatalf("invalid scope error = %T %v", err, err)
	}
}

func TestCompilePathGlob(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"*.go", "internal/db/schema.go", true},
		{"src/**/*.ts", "src/store.ts", true},
		{"src/**/*.ts", "src/lib/store.ts", true},
		{"internal/db/**", "internal/http/schema.go", false},
		{"internal/db/**", "internal/db/schema.go", true},
		{"internal/[dt]b/**", "internal/db/schema.go", true},
	}
	for _, tt := range tests {
		if got := pathMatchesFilter(tt.path, tt.pattern); got != tt.want {
			t.Errorf("pathMatchesFilter(%q, %q) = %v, want %v", tt.path, tt.pattern, got, tt.want)
		}
	}
}
