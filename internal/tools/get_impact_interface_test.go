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

// TestGetImpactInterfaceDispatch verifies that a change to one concrete
// implementation's method (Email.Send) reaches the caller that invokes it
// THROUGH the interface (Notify(n Notifier){ n.Send() }).
//
// Notifier has two implementations, so this call site's fan-out is narrow and
// the default confidence admits it. The default used to exclude every dispatch
// edge, which meant a method reached only through an interface reported no
// callers at all; the ceiling probe measured that as a loss against plain grep
// (experiments/eval/benchmarks/in-house/DISPATCH.md). Wide fan-outs are still
// excluded by default — see TestGetImpactWideInterfaceDispatchExcludedByDefault.
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

	// Default (no min_confidence): a two-implementation fan-out is narrow, so
	// the caller that only reaches Send through the interface is included.
	if got, via := impactedHas(""); !got {
		t.Errorf("at default confidence: Notify should be included (fan-out of 2 is narrow), via=%q", via)
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
	if !callChainHasNotify("") {
		t.Errorf("get_call_chain at default: Notify should be included (fan-out of 2 is narrow)")
	}
	if callChainHasNotify("receiver-typed") {
		t.Errorf("get_call_chain at high confidence: Notify should be excluded")
	}
}

// A wide fan-out stays excluded at the default confidence. Measured on gin and
// client_golang, admitting every dispatch edge grew individual responses by 872%
// and 1138%, and the blowups were exactly the wide call sites — an interface
// with many implementations turns one call into a list of everything that could
// conceivably run. The narrow cluster is worth admitting; this tail is not.
func TestGetImpactWideInterfaceDispatchExcludedByDefault(t *testing.T) {
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
	// Eight implementations: above maxDefaultDispatchWidth.
	for _, n := range []string{"A", "B", "C", "D", "E", "F", "G", "H"} {
		write("impl_"+n+".go", "package notify\n\ntype "+n+" struct{}\n\nfunc (x *"+n+") Send(msg string) error { return nil }\n")
	}
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

	cc := GetCallChainHandler(database)
	callers := func(minConfidence string) (names []string, hidden float64) {
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
			Excluded float64 `json:"interface_dispatch_excluded"`
		}
		if err := json.Unmarshal([]byte(items[0].Text), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		for _, c := range out.Callers {
			names = append(names, c.Name)
		}
		return names, out.Excluded
	}

	def, hidden := callers("")
	for _, n := range def {
		if n == "Notify" {
			t.Errorf("a fan-out of 8 exceeds the width gate; Notify must not appear at default confidence")
		}
	}
	// Excluding it silently is the bug this replaced: an empty list and "there
	// are no callers" have to stay distinguishable.
	if hidden == 0 {
		t.Error("wide dispatch edges were hidden without reporting interface_dispatch_excluded")
	}

	opted, _ := callers("interface-dispatch")
	found := false
	for _, n := range opted {
		if n == "Notify" {
			found = true
		}
	}
	if !found {
		t.Error("opting in with min_confidence must still reveal the wide fan-out")
	}
}
