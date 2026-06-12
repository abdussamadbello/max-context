package tools

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/artifacts"
	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/mcp"
)

// TestQueryCodebaseCarriesStalenessUnderFailure proves a tool response surfaces
// index health: with a recorded indexing failure, query_codebase's payload
// reports staleness.healthy=false and a staleness_warning, instead of silently
// returning results as if the index were current.
func TestQueryCodebaseCarriesStalenessUnderFailure(t *testing.T) {
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

	// Healthy first: no failures recorded.
	h := QueryCodebaseHandler(database, q, "")
	if got := stalenessOf(t, h, `{"query":"anything"}`); got["healthy"] != true {
		t.Fatalf("healthy index reported staleness.healthy=%v, want true", got["healthy"])
	}

	// Inject a failure through the same seam the worker uses.
	artifacts.RecordIndexError(database, "broken.go", "parse failed")

	out := callQuery(t, h, `{"query":"anything"}`)
	stale, ok := out["staleness"].(map[string]interface{})
	if !ok {
		t.Fatalf("response missing staleness object: %v", out)
	}
	if stale["healthy"] != false {
		t.Errorf("staleness.healthy = %v, want false", stale["healthy"])
	}
	if ff, _ := stale["failed_files"].(float64); ff != 1 {
		t.Errorf("staleness.failed_files = %v, want 1", stale["failed_files"])
	}
	if _, ok := out["staleness_warning"].(string); !ok {
		t.Errorf("response missing staleness_warning under failure: %v", out)
	}
}

func callQuery(t *testing.T, h mcp.ToolHandler, args string) map[string]interface{} {
	t.Helper()
	resp, err := h(json.RawMessage(args))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	items := resp.([]mcp.ContentItem)
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(items[0].Text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func stalenessOf(t *testing.T, h mcp.ToolHandler, args string) map[string]interface{} {
	t.Helper()
	out := callQuery(t, h, args)
	stale, ok := out["staleness"].(map[string]interface{})
	if !ok {
		t.Fatalf("response missing staleness object: %v", out)
	}
	return stale
}
