package indexer

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
)

// indexSource writes a single source file into a temp project, runs a full
// index, and returns the open DB. Shared by the TS/Python/JS resolution tests.
func indexSource(t *testing.T, filename, content string) *sql.DB {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(root, ".max-context", "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		t.Fatalf("PrepareQueries: %v", err)
	}
	t.Cleanup(func() { _ = q.Close() })
	if err := Index(context.Background(), root, database, q); err != nil {
		t.Fatalf("Index: %v", err)
	}
	return database
}

// --- 10a TypeScript ---

func TestTypeScriptResolution(t *testing.T) {
	src := `class Conn {
  query(): void {}
}
class Server {
  private db: Conn;
  handle(c: Conn): void {
    this.db.query();   // field receiver -> Conn.query
    c.query();         // typed param -> Conn.query
    this.helper();     // direct method on Server
  }
  helper(): void {}
}
function makeConn(): Conn { return new Conn(); }
function run(): void {
  const x = makeConn();  // return-typed local
  x.query();
  const s = new Conn();  // new local
  s.query();
}
`
	database := indexSource(t, "app.ts", src)

	// Every query() call should resolve receiver-typed to Conn (or Server.helper).
	rows, err := database.Query(`SELECT callee_name, resolution FROM calls WHERE callee_name IN ('query','helper') ORDER BY line`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var n, resolved int
	for rows.Next() {
		var cn, res string
		if err := rows.Scan(&cn, &res); err != nil {
			t.Fatal(err)
		}
		n++
		if res == "receiver-typed" {
			resolved++
		} else {
			t.Logf("edge %s resolved as %q (expected receiver-typed)", cn, res)
		}
	}
	if n == 0 {
		t.Fatal("no query/helper edges indexed")
	}
	// All five method calls (4×query + 1×helper) should be receiver-typed.
	if resolved < n {
		t.Errorf("only %d/%d TS method edges receiver-typed", resolved, n)
	}

	// Zero TS edges should fall back to name-global (the language is modeled).
	var ng int
	database.QueryRow(`SELECT COUNT(*) FROM calls e JOIN functions f ON f.id=e.caller_id WHERE f.language='typescript' AND e.resolution='name-global'`).Scan(&ng)
	if ng != 0 {
		t.Errorf("TS edges resolved name-global: %d (want 0)", ng)
	}
}

// --- 10b Python ---

func TestPythonResolution(t *testing.T) {
	src := `class Conn:
    def query(self):
        pass

class Server:
    db: Conn
    def __init__(self):
        self.cache = Conn()
    def handle(self, c: Conn):
        self.db.query()      # annotated field -> Conn.query
        self.cache.query()   # __init__ assignment -> Conn.query
        c.query()            # typed param -> Conn.query
        self.helper()        # direct method on Server
    def helper(self):
        pass
`
	database := indexSource(t, "app.py", src)

	rows, err := database.Query(`SELECT callee_name, resolution FROM calls WHERE callee_name IN ('query','helper') ORDER BY line`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var n, resolved int
	for rows.Next() {
		var cn, res string
		if err := rows.Scan(&cn, &res); err != nil {
			t.Fatal(err)
		}
		n++
		if res == "receiver-typed" {
			resolved++
		} else {
			t.Logf("edge %s resolved as %q (expected receiver-typed)", cn, res)
		}
	}
	if n == 0 {
		t.Fatal("no query/helper edges indexed")
	}
	if resolved < n {
		t.Errorf("only %d/%d Python method edges receiver-typed", resolved, n)
	}
}

// --- JavaScript stays name-global (explicit non-goal) ---

func TestJavaScriptStaysNameGlobal(t *testing.T) {
	src := `function helper() {}
function run() {
  helper();
  const x = makeThing();
  x.go();
}
`
	database := indexSource(t, "app.js", src)

	// JS has no type info — every linked edge must be name-global, never a
	// precise marker (which would be a fabricated guess).
	rows, err := database.Query(`SELECT DISTINCT resolution FROM calls e JOIN functions f ON f.id=e.caller_id WHERE f.language='javascript'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var res string
		if err := rows.Scan(&res); err != nil {
			t.Fatal(err)
		}
		if res != "name-global" {
			t.Errorf("JS edge resolution = %q, want name-global only", res)
		}
	}
}
