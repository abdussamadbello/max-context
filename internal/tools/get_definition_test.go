package tools

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/mcp"
)

func defTestDB(t *testing.T) (*sql.DB, *db.Queries, func()) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		t.Fatal(err)
	}
	return database, q, func() { q.Close(); database.Close() }
}

func TestGetDefinition_SingleExact(t *testing.T) {
	database, q, cleanup := defTestDB(t)
	defer cleanup()
	if _, err := q.InsertFunction.Exec("URL", "httpx/_urls.py", 15, 40, "python", 1, "", "", "URL"); err != nil {
		t.Fatal(err)
	}
	h := GetDefinitionHandler(database)
	resp, err := h(json.RawMessage(`{"symbol":"URL"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	out := parseDef(t, resp)
	if out.Total != 1 {
		t.Fatalf("expected 1 match, got %d", out.Total)
	}
	if !strings.Contains(out.Answer, "httpx/_urls.py:15") {
		t.Errorf("answer should cite the location decisively: %q", out.Answer)
	}
	if !strings.Contains(out.Answer, "answer now") {
		t.Errorf("answer should signal 'stop searching': %q", out.Answer)
	}
}

func TestGetDefinition_NotFound(t *testing.T) {
	database, _, cleanup := defTestDB(t)
	defer cleanup()
	h := GetDefinitionHandler(database)
	resp, err := h(json.RawMessage(`{"symbol":"Nonexistent"}`))
	if err != nil {
		t.Fatal(err)
	}
	out := parseDef(t, resp)
	if out.Total != 0 {
		t.Errorf("expected 0 matches, got %d", out.Total)
	}
	if !strings.Contains(out.Answer, "query_codebase") {
		t.Errorf("not-found answer should redirect to query_codebase: %q", out.Answer)
	}
}

func TestGetDefinition_Ambiguous(t *testing.T) {
	database, q, cleanup := defTestDB(t)
	defer cleanup()
	q.InsertFunction.Exec("Run", "internal/setup/setup.go", 14, 30, "go", 1, "", "", "Run")
	q.InsertFunction.Exec("Run", "internal/bench/runner.go", 54, 80, "go", 1, "", "", "Run")
	h := GetDefinitionHandler(database)
	resp, err := h(json.RawMessage(`{"symbol":"Run"}`))
	if err != nil {
		t.Fatal(err)
	}
	out := parseDef(t, resp)
	if out.Total != 2 {
		t.Fatalf("expected 2 matches, got %d", out.Total)
	}
	if !strings.Contains(out.Answer, "2 places") {
		t.Errorf("ambiguous answer should state the count: %q", out.Answer)
	}
}

func TestGetDefinition_OverloadedNamePrefersCanonicalType(t *testing.T) {
	database, q, cleanup := defTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`
		INSERT INTO functions (name, file_path, start_line, end_line, language, exported, code, docstring, signature, kind)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"URL", "httpx/_models.py", 44, 52, "python", 1, "", "", "URL", "method"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.InsertType.Exec("URL", "httpx/_urls.py", "class", "class URL:", 1); err != nil {
		t.Fatal(err)
	}

	h := GetDefinitionHandler(database)
	resp, err := h(json.RawMessage(`{"symbol":"URL"}`))
	if err != nil {
		t.Fatal(err)
	}
	out := parseDef(t, resp)
	if out.AnswerStatus != "definitive" {
		t.Fatalf("expected definitive answer, got %q", out.AnswerStatus)
	}
	if out.RecommendedNextAction != "answer_now" {
		t.Fatalf("expected answer_now action, got %q", out.RecommendedNextAction)
	}
	if got := out.Canonical["file"]; got != "httpx/_urls.py" {
		t.Fatalf("expected canonical type file, got %v", got)
	}
	if len(out.SecondaryMatches) != 1 {
		t.Fatalf("expected secondary method match, got %d", len(out.SecondaryMatches))
	}
	if got := out.Matches[0]["canonical"]; got != true {
		t.Fatalf("expected canonical match first, got %v", out.Matches[0])
	}
}

type defOut struct {
	Matches               []map[string]interface{} `json:"matches"`
	Total                 int                      `json:"total"`
	Answer                string                   `json:"answer"`
	AnswerStatus          string                   `json:"answer_status"`
	RecommendedNextAction string                   `json:"recommended_next_action"`
	Canonical             map[string]interface{}   `json:"canonical"`
	SecondaryMatches      []map[string]interface{} `json:"secondary_matches"`
}

func parseDef(t *testing.T, resp interface{}) defOut {
	t.Helper()
	items := resp.([]mcp.ContentItem)
	var out defOut
	if err := json.Unmarshal([]byte(items[0].Text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}
