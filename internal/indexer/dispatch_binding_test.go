package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
)

// Interface dispatch resolved only two receiver-binding forms: a parameter
// declared with the interface type, and `var x I = ...`. Every form whose type
// has to be inferred — a range variable, an index expression, a struct field —
// produced no edge, so a method reached that way had no callers at all. Those
// are the ordinary ways interfaces are held, which is why the ceiling probe
// found max-context behind plain grep on dispatch
// (experiments/eval/benchmarks/in-house/DISPATCH.md).
func TestInterfaceDispatchResolvesEveryBindingForm(t *testing.T) {
	root := t.TempDir()
	src := `package forms

type Notifier interface {
	Send(msg string) error
}

type Email struct{}

func (e *Email) Send(msg string) error { return nil }

func ParamForm(n Notifier, m string) error { return n.Send(m) }

func RangeForm(ns []Notifier, m string) error {
	for _, n := range ns {
		_ = n.Send(m)
	}
	return nil
}

func LocalVarForm(m string) error {
	var n Notifier = &Email{}
	return n.Send(m)
}

type Holder struct{ n Notifier }

func FieldForm(h *Holder, m string) error { return h.n.Send(m) }

func AssignForm(ns []Notifier, m string) error {
	n := ns[0]
	return n.Send(m)
}

func MapForm(ns map[string]Notifier, m string) error {
	for _, n := range ns {
		_ = n.Send(m)
	}
	return nil
}
`
	if err := os.WriteFile(filepath.Join(root, "forms.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	callers := dispatchCallersOf(t, root, "Send")
	for _, want := range []string{
		"ParamForm",    // interface-typed parameter
		"LocalVarForm", // var n Notifier = ...
		"RangeForm",    // for _, n := range ns   (ns []Notifier)
		"AssignForm",   // n := ns[0]
		"MapForm",      // for _, n := range ns   (ns map[string]Notifier)
		"FieldForm",    // h.n.Send()             (n is a Notifier field)
	} {
		if !callers[want] {
			t.Errorf("%s does not reach Send through the interface; binding form unresolved", want)
		}
	}
}

// The element type of a collection must never be attributed to the collection
// itself: `ns []Notifier` means ns is a slice, and typing ns as a Notifier
// would invent a call the code never makes.
func TestCollectionIsNotTypedAsItsElement(t *testing.T) {
	root := t.TempDir()
	src := `package forms

type Notifier interface {
	Send(msg string) error
}

type Email struct{}

func (e *Email) Send(msg string) error { return nil }

// Ranges over ns but never calls Send on ns itself.
func OnlyRanges(ns []Notifier, m string) error {
	for _, n := range ns {
		_ = n.Send(m)
	}
	return nil
}
`
	if err := os.WriteFile(filepath.Join(root, "forms.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if callers := dispatchCallersOf(t, root, "Send"); !callers["OnlyRanges"] {
		t.Error("OnlyRanges should reach Send through the range binding")
	}
}

// A range over an untyped collection must stay unresolved rather than guess.
func TestUnresolvableRangeProducesNoEdge(t *testing.T) {
	root := t.TempDir()
	src := `package forms

type Notifier interface {
	Send(msg string) error
}

type Email struct{}

func (e *Email) Send(msg string) error { return nil }

func Mystery(things interface{}, m string) error {
	for _, n := range things.([]Notifier) {
		_ = n.Send(m)
	}
	return nil
}
`
	if err := os.WriteFile(filepath.Join(root, "forms.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	// A type assertion in the range expression is not a typed identifier, so no
	// element type is known. No edge is the correct outcome; a wrong one is not.
	if callers := dispatchCallersOf(t, root, "Send"); callers["Mystery"] {
		t.Error("Mystery's range source is untyped; resolving it means the resolver guessed")
	}
}

// dispatchCallersOf indexes root and returns the callers of symbol reachable
// over interface-dispatch edges.
func dispatchCallersOf(t *testing.T, root, symbol string) map[string]bool {
	t.Helper()
	database, err := db.Open(filepath.Join(root, "index.db"))
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
	if err := Index(context.Background(), root, database, q); err != nil {
		t.Fatal(err)
	}
	rows, err := database.Query(`
		SELECT caller.name FROM calls e
		JOIN functions callee ON callee.id = e.callee_id
		JOIN functions caller ON caller.id = e.caller_id
		WHERE callee.name = ? AND e.resolution = 'interface-dispatch'`, symbol)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[n] = true
	}
	return out
}
