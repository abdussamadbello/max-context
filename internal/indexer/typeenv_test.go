package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
)

// One test, three languages. That shape is the point: the element-binding logic
// used to live inside each parse function, with the record types declared
// locally, so it was written twice identically and Python was simply missing a
// rule the other two had — with nothing in the output to say so. A single test
// could not have covered all three because there was no single implementation
// to cover.
func TestRangeBindingResolvesInEveryDeepLanguage(t *testing.T) {
	for _, tc := range []struct {
		name, file, src string
		method          string
		wantCaller      string
	}{
		{
			name: "go", file: "app.go", method: "Send", wantCaller: "DeliverAll",
			src: `package app

type Emailer struct{}

func (e *Emailer) Send(msg string) error { return nil }

func DeliverAll(ns []Emailer, msg string) {
	for _, n := range ns {
		_ = n.Send(msg)
	}
}
`,
		},
		{
			name: "typescript", file: "app.ts", method: "send", wantCaller: "deliverAll",
			src: `export class Emailer {
  send(msg: string): void {}
}

export function deliverAll(ns: Emailer[], msg: string): void {
  for (const n of ns) {
    n.send(msg);
  }
}
`,
		},
		{
			name: "python", file: "app.py", method: "send", wantCaller: "deliver_all",
			src: `from typing import List


class Emailer:
    def send(self, msg: str) -> None:
        pass


def deliver_all(ns: List[Emailer], msg: str) -> None:
    for n in ns:
        n.send(msg)
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, tc.file), []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			if callers := callersOfMethod(t, root, tc.method); !callers[tc.wantCaller] {
				t.Errorf("%s: %s calls %s through a range binding but does not resolve; got %v",
					tc.name, tc.wantCaller, tc.method, keysOf(callers))
			}
		})
	}
}

// A collection with no recorded type must leave its binding untyped. Guessing
// here would manufacture a call the source does not make, in every language at
// once now that the rule is shared.
func TestUntypedCollectionBindsNothing(t *testing.T) {
	for _, tc := range []struct{ name, file, src, method, caller string }{
		{
			name: "python", file: "app.py", method: "send", caller: "deliver_untyped",
			src: "class Emailer:\n    def send(self, msg: str) -> None:\n        pass\n\n\ndef deliver_untyped(ns, msg):\n    for n in ns:\n        n.send(msg)\n",
		},
		{
			name: "typescript", file: "app.ts", method: "send", caller: "deliverUntyped",
			src: "export class Emailer {\n  send(msg: string): void {}\n}\n\nexport function deliverUntyped(ns: any, msg: string): void {\n  for (const n of ns) {\n    n.send(msg);\n  }\n}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, tc.file), []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			if callers := callersOfMethod(t, root, tc.method); callers[tc.caller] {
				t.Errorf("%s: %s ranges over an untyped collection; resolving it means the parser guessed",
					tc.name, tc.caller)
			}
		})
	}
}

// bindElementTypes must not attribute the element type to the collection
// itself: `ns []Emailer` means ns is a slice, and typing ns as an Emailer would
// resolve `ns.Send()` — a call the source never makes.
func TestBindElementTypesLeavesTheCollectionUntyped(t *testing.T) {
	spans := []*funcSpan{{start: 1, end: 10, types: map[string]string{}}}
	bindElementTypes(spans,
		[]typedIdent{{name: "ns", typ: "Emailer", line: 2}},
		[]derivedBinding{{name: "n", src: "ns", line: 3}})

	if got := spans[0].types["n"]; got != "Emailer" {
		t.Errorf("binding n = %q, want Emailer", got)
	}
	if got, present := spans[0].types["ns"]; present {
		t.Errorf("collection ns was typed as %q; it is a slice, not an element", got)
	}
}

// A binding whose source has no element type recorded must stay absent, not be
// written as an empty string — an empty type would compare equal to "no type"
// in some places and to a real lookup miss in others.
func TestBindElementTypesSkipsUnknownSources(t *testing.T) {
	spans := []*funcSpan{{start: 1, end: 10, types: map[string]string{}}}
	bindElementTypes(spans, []typedIdent{{name: "other", typ: "Emailer", line: 2}},
		[]derivedBinding{{name: "n", src: "ns", line: 3}})
	if _, present := spans[0].types["n"]; present {
		t.Error("n was bound from a source with no recorded element type")
	}
}

func callersOfMethod(t *testing.T, root, method string) map[string]bool {
	t.Helper()
	database, err := db.Open(filepath.Join(root, ".max-context", "index.db"))
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
		WHERE callee.name = ?`, method)
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

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
