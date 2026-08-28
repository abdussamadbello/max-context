package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/indexer"
	"github.com/maxcontext/max-context/internal/mcp"
)

// "What implements this?" and "who calls this?" are different questions. Before
// the implementations relation existed, satisfaction was reachable only as a
// fan-out of synthetic call edges inside an answer to the other question — so
// asking it directly was impossible, and the fan-out had to be width-gated to
// keep caller lists usable.
func TestImplementationsIsItsOwnQuery(t *testing.T) {
	database, cc := indexedDispatchFixture(t)
	defer database.Close()

	impls := implementationsFor(t, cc, "Send")
	byType := map[string]string{}
	for _, i := range impls {
		byType[i.Type] = i.Interface
	}
	for _, want := range []string{"EmailNotifier", "SMSNotifier"} {
		if byType[want] != "Notifier" {
			t.Errorf("%s should satisfy Notifier, got %q", want, byType[want])
		}
	}
	// The decoy satisfies Notifier structurally — it has a Send method — even
	// though no call site ever passes one. Listing it is correct and is what
	// explains its presence in the caller list; hiding it would make that
	// presence unexplainable.
	if byType["MetricsBuffer"] != "Notifier" {
		t.Error("MetricsBuffer structurally satisfies Notifier and must be listed")
	}
	for _, i := range impls {
		if i.FilePath == "" || i.Line == 0 {
			t.Errorf("implementation %s has no location: %+v", i.Type, i)
		}
		if i.Symbol == "" {
			t.Errorf("implementation %s carries no symbol, so it cannot be addressed precisely", i.Type)
		}
	}
}

// An interface method nothing satisfies must say so, rather than returning an
// empty list that reads identically to "this index does not record that".
func TestImplementationsExplainsAnEmptyResult(t *testing.T) {
	database, cc := indexedDispatchFixture(t)
	defer database.Close()

	raw, _ := json.Marshal(map[string]interface{}{
		"function_name": "NoSuchMethodAnywhere", "direction": "implementations",
	})
	resp, err := cc(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("get_call_chain: %v", err)
	}
	var out struct {
		Implementations []implementationHit `json:"implementations"`
		Note            string              `json:"note"`
	}
	if err := json.Unmarshal([]byte(resp.([]mcp.ContentItem)[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Implementations) != 0 {
		t.Errorf("expected no implementations, got %d", len(out.Implementations))
	}
	if out.Note == "" {
		t.Error("an empty result must explain itself; silence reads as 'not recorded'")
	}
}

// Asking for implementations must not also return callers: the whole point is
// that the two questions stop being answered by one query.
func TestImplementationsDoesNotReturnCallers(t *testing.T) {
	database, cc := indexedDispatchFixture(t)
	defer database.Close()

	raw, _ := json.Marshal(map[string]interface{}{
		"function_name": "Send", "direction": "implementations",
	})
	resp, err := cc(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("get_call_chain: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(resp.([]mcp.ContentItem)[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"callers", "callees"} {
		if _, present := out[k]; present {
			t.Errorf("implementations response also carries %q", k)
		}
	}
}

func implementationsFor(t *testing.T, cc mcp.ToolHandler, method string) []implementationHit {
	t.Helper()
	raw, _ := json.Marshal(map[string]interface{}{
		"function_name": method, "direction": "implementations",
	})
	resp, err := cc(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("get_call_chain: %v", err)
	}
	var out struct {
		Implementations []implementationHit `json:"implementations"`
	}
	if err := json.Unmarshal([]byte(resp.([]mcp.ContentItem)[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	return out.Implementations
}

// indexedDispatchFixture builds the Notifier/EmailNotifier/SMSNotifier repo
// with the MetricsBuffer decoy, indexed and ready to query.
func indexedDispatchFixture(t *testing.T) (*sql.DB, mcp.ToolHandler) {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		"notifier.go": "package notify\n\ntype Notifier interface {\n\tSend(msg string) error\n}\n",
		"email.go":    "package notify\n\ntype EmailNotifier struct{}\n\nfunc (e *EmailNotifier) Send(msg string) error { return nil }\n",
		"sms.go":      "package notify\n\ntype SMSNotifier struct{}\n\nfunc (s *SMSNotifier) Send(msg string) error { return nil }\n",
		"metrics.go":  "package notify\n\ntype MetricsBuffer struct{}\n\nfunc (m *MetricsBuffer) Send(msg string) error { return nil }\n\nfunc FlushMetrics(m *MetricsBuffer) error { return m.Send(\"x\") }\n",
		"pipeline.go": "package notify\n\nfunc DeliverAlert(n Notifier) error { return n.Send(\"hi\") }\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	database, err := db.Open(filepath.Join(root, ".max-context", "index.db"))
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
	t.Cleanup(func() { q.Close() })
	if err := indexer.Index(context.Background(), root, database, q); err != nil {
		t.Fatal(err)
	}
	return database, GetCallChainHandler(database)
}
