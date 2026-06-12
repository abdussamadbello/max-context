package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/indexer"
	"github.com/maxcontext/max-context/internal/mcp"
)

// TestGetImpactInterfaceDispatch verifies Phase 5: a change to one concrete
// implementation's method (Email.Send) reaches the caller that invokes it
// THROUGH the interface (Notify(n Notifier){ n.Send() }) — but only at low
// min_confidence. At default (high) confidence the low-confidence interface
// fan-out is excluded, so default blast radius is unchanged.
func TestGetImpactInterfaceDispatch(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("notifier.go", `package notify

type Notifier interface {
	Send(msg string) error
}
`)
	write("email.go", `package notify

type Email struct{}

func (e *Email) Send(msg string) error { return nil }
`)
	write("sms.go", `package notify

type SMS struct{}

func (s *SMS) Send(msg string) error { return nil }
`)
	write("caller.go", `package notify

func Notify(n Notifier) {
	n.Send("hi")
}
`)

	database, err := db.Open(filepath.Join(root, ".max-context", "index.db"))
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
	if err := indexer.Index(ctx, root, database, q); err != nil {
		t.Fatalf("Index: %v", err)
	}

	store := db.NewSQLiteStore(database)
	h := GetImpactHandler(store, root)

	impactedHas := func(minConfidence string) (bool, string) {
		args := map[string]interface{}{"files": []string{"email.go"}, "direction": "callers"}
		if minConfidence != "" {
			args["min_confidence"] = minConfidence
		}
		raw, _ := json.Marshal(args)
		resp, err := h(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("get_impact(%q): %v", minConfidence, err)
		}
		items := resp.([]mcp.ContentItem)
		var out struct {
			Impacted []struct {
				Symbol        string `json:"symbol"`
				ViaResolution string `json:"via_resolution"`
			} `json:"impacted"`
		}
		if err := json.Unmarshal([]byte(items[0].Text), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		for _, n := range out.Impacted {
			if n.Symbol == "Notify" {
				return true, n.ViaResolution
			}
		}
		return false, ""
	}

	// Low confidence: the interface call site IS included, labeled interface-dispatch.
	got, via := impactedHas("interface-dispatch")
	if !got {
		t.Errorf("at low confidence: expected Notify in blast radius via interface dispatch, missing")
	}
	if via != "interface-dispatch" {
		t.Errorf("interface call site via_resolution = %q, want interface-dispatch", via)
	}

	// Default (no min_confidence): excluded — default behavior unchanged.
	if got, _ := impactedHas(""); got {
		t.Errorf("at default confidence: Notify should be excluded (interface dispatch is opt-in)")
	}

	// High confidence: also excluded.
	if got, _ := impactedHas("receiver-typed"); got {
		t.Errorf("at high confidence: Notify should be excluded")
	}

	// get_call_chain gates interface-dispatch the same way: callers of Email.Send
	// include the interface caller Notify only at low confidence.
	cc := GetCallChainHandler(database)
	callChainHasNotify := func(minConfidence string) bool {
		args := map[string]interface{}{"function_name": "Send", "direction": "callers"}
		if minConfidence != "" {
			args["min_confidence"] = minConfidence
		}
		raw, _ := json.Marshal(args)
		resp, err := cc(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("get_call_chain(%q): %v", minConfidence, err)
		}
		items := resp.([]mcp.ContentItem)
		var out struct {
			Callers []struct {
				Name string `json:"name"`
			} `json:"callers"`
		}
		if err := json.Unmarshal([]byte(items[0].Text), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		for _, n := range out.Callers {
			if n.Name == "Notify" {
				return true
			}
		}
		return false
	}
	if !callChainHasNotify("interface-dispatch") {
		t.Errorf("get_call_chain at low confidence: expected Notify caller via interface dispatch")
	}
	if callChainHasNotify("") {
		t.Errorf("get_call_chain at default: Notify should be excluded (interface dispatch is opt-in)")
	}
	if callChainHasNotify("receiver-typed") {
		t.Errorf("get_call_chain at high confidence: Notify should be excluded")
	}
}
