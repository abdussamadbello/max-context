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

type callChainResponse struct {
	Callers      []map[string]interface{} `json:"callers"`
	Callees      []map[string]interface{} `json:"callees"`
	CallersTotal int                      `json:"callers_total"`
	Truncated    bool                     `json:"truncated"`
	NextAction   string                   `json:"recommended_next_action"`
	Note         string                   `json:"note"`
}

// hubFixture builds a function called by n others, which is the shape that made
// get_call_chain return 3,272 tokens in one call.
func hubFixture(t *testing.T, callers int) (*sql.DB, mcp.ToolHandler) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "chain.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Close() })

	res, err := q.InsertFunction.Exec("Hub", "hub.go", 1, 5, "go", 1, "", "", "Hub()")
	if err != nil {
		t.Fatal(err)
	}
	hub, _ := res.LastInsertId()
	for i := 0; i < callers; i++ {
		name := fmt.Sprintf("Caller%03d", i)
		r, err := q.InsertFunction.Exec(name, fmt.Sprintf("c%03d.go", i), 1, 5, "go", 1, "", "", name+"()")
		if err != nil {
			t.Fatal(err)
		}
		id, _ := r.LastInsertId()
		if _, err := q.InsertCall.Exec(id, hub, "Hub", fmt.Sprintf("c%03d.go", i), 3); err != nil {
			t.Fatal(err)
		}
	}
	return database, GetCallChainHandler(database)
}

func callChain(t *testing.T, h mcp.ToolHandler, args string) callChainResponse {
	t.Helper()
	resp, err := h(json.RawMessage(args))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var out callChainResponse
	if err := json.Unmarshal([]byte(resp.([]mcp.ContentItem)[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// The measurement that motivated this: tracing callers of a widely-called
// symbol returned every one of them, unbounded, in a tool whose whole claim is
// context efficiency.
func TestCallChainCapsResults(t *testing.T) {
	_, h := hubFixture(t, 300)

	out := callChain(t, h, `{"function_name":"Hub","direction":"callers"}`)
	if len(out.Callers) != defaultCallChainResults {
		t.Errorf("returned %d callers, want the default cap of %d", len(out.Callers), defaultCallChainResults)
	}
}

// A cap the model cannot see is worse than no cap: it would report a partial
// blast radius as the whole of it.
func TestCallChainReportsTruncation(t *testing.T) {
	_, h := hubFixture(t, 300)

	out := callChain(t, h, `{"function_name":"Hub","direction":"callers"}`)
	if !out.Truncated {
		t.Error("truncated flag not set on a capped answer")
	}
	if out.CallersTotal != 300 {
		t.Errorf("callers_total = %d, want the true total 300", out.CallersTotal)
	}
	if out.NextAction != actionNarrowScope {
		t.Errorf("recommended_next_action = %q, want %q", out.NextAction, actionNarrowScope)
	}
	if out.Note == "" {
		t.Error("no note explaining how to get more or fewer results")
	}
}

// An answer that fits must not claim to be capped.
func TestCallChainDoesNotClaimTruncationWhenComplete(t *testing.T) {
	_, h := hubFixture(t, 5)

	out := callChain(t, h, `{"function_name":"Hub","direction":"callers"}`)
	if len(out.Callers) != 5 {
		t.Errorf("got %d callers, want all 5", len(out.Callers))
	}
	if out.Truncated {
		t.Error("complete answer marked truncated")
	}
	if out.CallersTotal != 0 {
		t.Errorf("callers_total set (%d) on a complete answer", out.CallersTotal)
	}
}

func TestCallChainHonoursMaxResults(t *testing.T) {
	_, h := hubFixture(t, 300)

	for _, tc := range []struct{ arg, want int }{
		{10, 10},
		{200, 200},
		{500, maxCallChainResults}, // clamped, not obeyed blindly
		{0, 1},                     // floor
	} {
		out := callChain(t, h, fmt.Sprintf(`{"function_name":"Hub","direction":"callers","max_results":%d}`, tc.arg))
		if len(out.Callers) != tc.want {
			t.Errorf("max_results=%d returned %d callers, want %d", tc.arg, len(out.Callers), tc.want)
		}
	}
}

// The cap must keep the nearest callers, not an arbitrary slice: rows arrive
// ordered by depth, so depth-1 callers must survive it.
func TestCallChainCapKeepsNearestFirst(t *testing.T) {
	_, h := hubFixture(t, 300)

	out := callChain(t, h, `{"function_name":"Hub","direction":"callers","max_results":5}`)
	for _, c := range out.Callers {
		if d, ok := c["depth"].(float64); ok && d != 1 {
			t.Errorf("cap kept a depth-%v caller while depth-1 callers exist", d)
		}
	}
}

// The response is the product; a cap is only worth having if it actually shrinks it.
func TestCallChainCapShrinksTheResponse(t *testing.T) {
	_, h := hubFixture(t, 300)

	capped, err := h(json.RawMessage(`{"function_name":"Hub","direction":"callers"}`))
	if err != nil {
		t.Fatal(err)
	}
	full, err := h(json.RawMessage(`{"function_name":"Hub","direction":"callers","max_results":200}`))
	if err != nil {
		t.Fatal(err)
	}
	small := len(capped.([]mcp.ContentItem)[0].Text)
	large := len(full.([]mcp.ContentItem)[0].Text)
	if small >= large {
		t.Errorf("capped response (%d bytes) is not smaller than the wider one (%d bytes)", small, large)
	}
	t.Logf("default cap: %d bytes vs max_results=200: %d bytes", small, large)
}
