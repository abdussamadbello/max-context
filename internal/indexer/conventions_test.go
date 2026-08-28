package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
)

// Interface detection was `definition LIKE 'interface%'` — Go syntax — so no
// other language recorded a single implementation, however deeply it was
// otherwise indexed.
func TestIsInterfaceTypeIsLanguageParameterised(t *testing.T) {
	for _, tc := range []struct {
		name                string
		language, kind, def string
		want                bool
		why                 string
	}{
		{
			name: "go interface", language: "go", kind: "type", def: "interface {\n\tSend(msg string) error\n}",
			want: true, why: "the keyword is what separates a Go interface from a struct",
		},
		{
			name: "go struct is not an interface", language: "go", kind: "type", def: "struct {\n\taddr string\n}",
			want: false, why: "same kind, different construct",
		},
		{
			name: "typescript interface", language: "typescript", kind: "interface", def: "{\n  send(msg: string): void;\n}",
			want: true, why: "a TS interface body starts with a brace, which the Go predicate rejected",
		},
		{
			name: "typescript type alias is not implementable", language: "typescript", kind: "type", def: "{ a: string }",
			want: false, why: "nothing can implement a type alias",
		},
		{
			name: "unknown language with a Go-shaped definition", language: "", kind: "type", def: "interface { Foo() }",
			want: true, why: "an unambiguous syntactic marker still identifies it",
		},
		{
			name: "unknown language, bare kind only", language: "", kind: "interface", def: "{ x }",
			want: false, why: "a bare kind from an unregistered language must not borrow another language's rule",
		},
		{
			name: "language with no convention", language: "rust", kind: "trait", def: "{ fn send(); }",
			want: false, why: "no convention written down means no relation, not a guessed one",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isInterfaceType(tc.language, tc.kind, tc.def); got != tc.want {
				t.Errorf("isInterfaceType(%q,%q,...) = %v, want %v — %s", tc.language, tc.kind, got, tc.want, tc.why)
			}
		})
	}
}

func TestSatisfactionRuleFor(t *testing.T) {
	if rule, ok := satisfactionRuleFor("go"); !ok || rule != SatisfactionStructural {
		t.Errorf("go should use the structural rule; the language states nothing to read")
	}
	if rule, ok := satisfactionRuleFor("typescript"); !ok || rule != SatisfactionDeclared {
		t.Errorf("typescript states `implements`, so satisfaction must be read, not inferred")
	}
	if _, ok := satisfactionRuleFor("cobol"); ok {
		t.Errorf("an unregistered language must report that it has no convention")
	}
}

// The payoff: TypeScript satisfaction is EXACT where Go's is structural. A class
// with a matching method but no `implements` clause is not an implementation,
// and saying so removes the false positive that costs precision on the Go
// fixture (experiments/eval/benchmarks/in-house/DISPATCH.md).
func TestTypeScriptSatisfactionIsDeclaredNotStructural(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"notifier.ts": "export interface Notifier {\n  send(msg: string): void;\n}\n",
		"email.ts":    "import { Notifier } from \"./notifier\";\nexport class EmailNotifier implements Notifier {\n  send(msg: string): void {}\n}\n",
		// Matching signature, no implements clause, never used as a Notifier.
		"metrics.ts": "export class MetricsBuffer {\n  send(msg: string): void {}\n}\n",
		"pipeline.ts": "import { Notifier } from \"./notifier\";\n" +
			"export function deliverAlert(n: Notifier, msg: string): void {\n  n.send(msg);\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	impls := implementationsIn(t, root)
	if !impls["EmailNotifier"] {
		t.Error("EmailNotifier declares `implements Notifier` and must be recorded")
	}
	if impls["MetricsBuffer"] {
		t.Error("MetricsBuffer declares no `implements`; a declared rule must not infer it from a matching method name")
	}
}

// Go keeps the structural rule, because Go states nothing to read. The same
// decoy that TypeScript excludes is correctly included here.
func TestGoSatisfactionStaysStructural(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"notifier.go": "package notify\n\ntype Notifier interface {\n\tSend(msg string) error\n}\n",
		"email.go":    "package notify\n\ntype Email struct{}\n\nfunc (e *Email) Send(msg string) error { return nil }\n",
		"metrics.go":  "package notify\n\ntype Metrics struct{}\n\nfunc (m *Metrics) Send(msg string) error { return nil }\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	impls := implementationsIn(t, root)
	for _, want := range []string{"Email", "Metrics"} {
		if !impls[want] {
			t.Errorf("%s covers Notifier's method set and must be recorded under the structural rule", want)
		}
	}
}

// implementationsIn indexes root and returns the set of implementing types.
func implementationsIn(t *testing.T, root string) map[string]bool {
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
	rows, err := database.Query("SELECT DISTINCT impl_type FROM implementations")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out[s] = true
	}
	return out
}
