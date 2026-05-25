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
