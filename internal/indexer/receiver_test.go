package indexer

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
)

// `base.field.method()` resolved in Go and in neither TypeScript nor Python,
// because Go typed the base from local scope while the other two assumed the
// base was self/this. One rule, three implementations, two of them missing half
// of it — with nothing in the output to say which half a given language applied.
func TestFieldReceiverResolvesInEveryDeepLanguage(t *testing.T) {
	for _, tc := range []struct {
		name, file, src, method, wantCaller string
	}{
		{
			name: "go", file: "a.go", method: "Query", wantCaller: "Run",
			src: `package a

type Conn struct{}

func (c *Conn) Query(s string) error { return nil }

type Holder struct{ db *Conn }

func Run(h *Holder) error { return h.db.Query("x") }
`,
		},
		{
			name: "typescript", file: "a.ts", method: "query", wantCaller: "run",
			src: `export class Conn {
  query(s: string): void {}
}

export class Holder {
  db: Conn;
}

export function run(h: Holder): void {
  h.db.query("x");
}
`,
		},
		{
			name: "python", file: "a.py", method: "query", wantCaller: "run",
			src: `class Conn:
    def query(self, s: str) -> None:
        pass


class Holder:
    db: Conn


def run(h: Holder) -> None:
    h.db.query("x")
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, tc.file), []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			if callers := callersOfMethod(t, root, tc.method); !callers[tc.wantCaller] {
				t.Errorf("%s: %s calls %s through a field on a parameter but does not resolve; got %v",
					tc.name, tc.wantCaller, tc.method, keysOf(callers))
			}
		})
	}
}

// self/this must keep resolving against the ENCLOSING CLASS, which is the half
// TypeScript and Python already had. Sharing the rule must not cost it.
func TestSelfAndThisFieldsStillResolveAgainstTheEnclosingClass(t *testing.T) {
	for _, tc := range []struct{ name, file, src, method, wantCaller string }{
		{
			name: "python self", file: "a.py", method: "query", wantCaller: "run",
			src: `class Conn:
    def query(self, s: str) -> None:
        pass


class Service:
    db: Conn

    def run(self) -> None:
        self.db.query("x")
`,
		},
		{
			name: "typescript this", file: "a.ts", method: "query", wantCaller: "run",
			src: `export class Conn {
  query(s: string): void {}
}

export class Service {
  db: Conn;
  run(): void {
    this.db.query("x");
  }
}
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, tc.file), []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			if callers := callersOfMethod(t, root, tc.method); !callers[tc.wantCaller] {
				t.Errorf("%s: %s does not resolve; got %v", tc.name, tc.wantCaller, keysOf(callers))
			}
		})
	}
}

// The dangerous half of the old behaviour: Python captured the real base and
// discarded it, so `h.db.query()` inside a method recorded a receiver of
// `self.db` and resolved against the ENCLOSING class's field. Where the two
// classes' fields differ in type, that is a confidently wrong answer — labelled
// receiver-typed — rather than a missing one.
func TestFieldBaseIsNotConfusedWithSelf(t *testing.T) {
	root := t.TempDir()
	src := `class RemoteConn:
    def query(self, s: str) -> None:
        pass


class LocalConn:
    def query(self, s: str) -> None:
        pass


class Holder:
    db: RemoteConn


class Service:
    db: LocalConn

    def run(self, h: Holder) -> None:
        h.db.query("x")
`
	if err := os.WriteFile(filepath.Join(root, "a.py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	owner, receiver := resolvedOwnerOf(t, root, "query")
	if receiver != "h.db" {
		t.Errorf("receiver recorded as %q, want %q — the base identifier was discarded", receiver, "h.db")
	}
	if owner != "RemoteConn" {
		t.Errorf("h.db.query() resolved to %s.query; h is a Holder, so the answer is RemoteConn.query", owner)
	}
}

// classifyFieldReceiver must leave an unknown base unresolved rather than
// falling back to the enclosing class, which is what produced the wrong answer.
func TestUnknownFieldBaseStaysUnresolved(t *testing.T) {
	spans := []*funcSpan{{start: 1, end: 10, types: map[string]string{}}}
	clsSpans := []*classSpan{{start: 1, end: 10, name: "Service"}}

	var cr CallRecord
	cr.classifyFieldReceiver("mystery", "db", selfKeyword("self"), spans, clsSpans, 5)
	if cr.ReceiverKind != "unresolved-field" {
		t.Errorf("kind = %q, want unresolved-field: an untyped base must not borrow the enclosing class", cr.ReceiverKind)
	}
	if cr.ReceiverType != "" {
		t.Errorf("type = %q, want empty", cr.ReceiverType)
	}

	var self CallRecord
	self.classifyFieldReceiver("self", "db", selfKeyword("self"), spans, clsSpans, 5)
	if self.ReceiverKind != "field" || self.ReceiverType != "Service" {
		t.Errorf("self.db = (%q,%q), want (field,Service)", self.ReceiverKind, self.ReceiverType)
	}
}

// resolvedOwnerOf returns the receiver type of the function a call resolved to,
// plus the recorded receiver name.
func resolvedOwnerOf(t *testing.T, root, callee string) (owner, receiver string) {
	t.Helper()
	database, q, cleanup := openIndexed(t, root)
	defer cleanup()
	_ = q
	row := database.QueryRow(`
		SELECT COALESCE(f.receiver_type, ''), COALESCE(e.receiver_name, '')
		FROM calls e JOIN functions f ON f.id = e.callee_id
		WHERE e.callee_name = ? LIMIT 1`, callee)
	if err := row.Scan(&owner, &receiver); err != nil {
		t.Fatalf("no resolved call to %s: %v", callee, err)
	}
	return owner, receiver
}

// openIndexed indexes root and returns the open database plus a cleanup.
func openIndexed(t *testing.T, root string) (*sql.DB, *db.Queries, func()) {
	t.Helper()
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
	if err := Index(context.Background(), root, database, q); err != nil {
		t.Fatal(err)
	}
	return database, q, func() { q.Close(); database.Close() }
}
