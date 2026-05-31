package indexer

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
)

// TestPythonCrossFileImportResolution verifies that a bare call to a name brought
// in via `from m import f` — INCLUDING the aliased form `from m import f as g` —
// resolves cross-file to the original symbol, marked import-symbol. This is the
// recall case grep cannot text-match: the call site (g(...)) shares no text with
// the definition (def f). Ambiguous origins must stay unresolved (no false edge).
func TestPythonCrossFileImportResolution(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	// utils.py: the definition site.
	writeFile(t, root, "utils.py", `def build_payload(data):
    return {"body": data}

def shared(x):
    return x
`)
	// other.py: a COLLIDING top-level name, to prove ambiguous origins don't link.
	writeFile(t, root, "other.py", `def shared(y):
    return y
`)
	// caller.py: plain import + aliased import + an ambiguous import.
	writeFile(t, root, "caller.py", `from utils import build_payload
from utils import build_payload as make_body
from utils import shared

def handle_plain(d):
    return build_payload(d)      # plain cross-file -> import-symbol

def handle_alias(d):
    return make_body(d)          # aliased cross-file -> import-symbol (origin build_payload)

def handle_ambiguous(d):
    return shared(d)             # 'shared' defined twice -> must stay unresolved
`)

	database := indexProject(t, ctx, root)

	// The plain call (by original name) and the aliased call (by local name
	// make_body) must BOTH resolve cross-file to utils.py:build_payload.
	assertResolvesTo(t, database, "build_payload", "import-symbol", "utils.py", "build_payload", 1)
	assertResolvesTo(t, database, "make_body", "import-symbol", "utils.py", "build_payload", 1)

	// The ambiguous 'shared' call must NOT be linked (callee_id NULL) — no false edge.
	var linked int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM calls WHERE callee_name='shared' AND callee_id IS NOT NULL`,
	).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != 0 {
		t.Errorf("ambiguous 'shared' must stay unresolved (no false edge); got %d linked", linked)
	}
}

// TestExternalImportNotLinked is the regression gate for the validated
// false-positive bug: a name imported from a STDLIB/3rd-party module must NOT
// link to a same-named local function, in either Python or TypeScript.
func TestExternalImportNotLinked(t *testing.T) {
	ctx := context.Background()

	t.Run("python-stdlib", func(t *testing.T) {
		root := t.TempDir()
		// Repo defines its own unquote AND imports the stdlib one elsewhere.
		writeFile(t, root, "localdefs.py", "def unquote(s):\n    return s\n")
		writeFile(t, root, "caller.py", "from urllib.parse import unquote\n\ndef handle(s):\n    return unquote(s)\n")
		database := indexProject(t, ctx, root)
		assertNoLink(t, database, "unquote", "caller.py")
	})

	t.Run("typescript-node-builtin", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "localdefs.ts", "export function readFileSync(p: string): string {\n  return p;\n}\n")
		writeFile(t, root, "caller.ts", "import { readFileSync } from \"node:fs\";\n\nexport function handle(p: string): string {\n  return readFileSync(p);\n}\n")
		database := indexProject(t, ctx, root)
		assertNoLink(t, database, "readFileSync", "caller.ts")
	})
}

// assertNoLink fails if any call to calleeName in callerFile is linked (callee_id
// non-NULL) — used to prove external imports produce no false edge.
func assertNoLink(t *testing.T, database *sql.DB, calleeName, callerFile string) {
	t.Helper()
	var n int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM calls WHERE callee_name=? AND file_path=? AND callee_id IS NOT NULL`,
		calleeName, callerFile,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%s in %s: expected 0 linked edges (external import), got %d false edge(s)", calleeName, callerFile, n)
	}
}

// TestTypeScriptCrossFileImportResolution verifies named imports — plain and
// renamed (`import { f as g }`) — resolve cross-file to the original symbol.
func TestTypeScriptCrossFileImportResolution(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeFile(t, root, "utils.ts", `export function buildPayload(data: string): object {
  return { body: data };
}
`)
	writeFile(t, root, "caller.ts", `import { buildPayload } from "./utils";
import { buildPayload as makeBody } from "./utils";

export function handlePlain(d: string): object {
  return buildPayload(d);
}
export function handleAlias(d: string): object {
  return makeBody(d);
}
`)

	database := indexProject(t, ctx, root)
	assertResolvesTo(t, database, "buildPayload", "import-symbol", "utils.ts", "buildPayload", 1)
	assertResolvesTo(t, database, "makeBody", "import-symbol", "utils.ts", "buildPayload", 1)
}

// TestGoCrossPackageResolution verifies pkg.Func() links to the imported
// package's Func (incl. an aliased package import), and that a same-named local
// function and an ambiguous package segment do NOT produce false edges.
func TestGoCrossPackageResolution(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeFile(t, root, "go.mod", "module example.com/x\n\ngo 1.21\n")
	writeFile(t, root, "util/util.go", `package util

func BuildPayload(data string) string { return "body:" + data }
`)
	writeFile(t, root, "app/main.go", `package main

import (
	"example.com/x/util"
	u "example.com/x/util"
)

func HandlePlain(d string) string { return util.BuildPayload(d) }
func HandleAlias(d string) string { return u.BuildPayload(d) }
func main() {}
`)

	database := indexProject(t, ctx, root)
	// Both the plain and aliased package-qualified calls resolve cross-package.
	assertResolvesTo(t, database, "BuildPayload", "cross-package", "util.go", "BuildPayload", 2)
}

// indexProject indexes a multi-file project rooted at root and returns the DB.
func indexProject(t *testing.T, ctx context.Context, root string) *sql.DB {
	t.Helper()
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
	if err := Index(ctx, root, database, q); err != nil {
		t.Fatalf("Index: %v", err)
	}
	return database
}

// assertResolvesTo checks that wantN calls to calleeName link, with the given
// resolution marker, to a definition named targetName in targetFile.
func assertResolvesTo(t *testing.T, database *sql.DB, calleeName, marker, targetFile, targetName string, wantN int) {
	t.Helper()
	rows, err := database.Query(`
		SELECT c.resolution, f.file_path, f.name
		FROM calls c JOIN functions f ON c.callee_id = f.id
		WHERE c.callee_name = ?`, calleeName)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := 0
	for rows.Next() {
		var res, file, name string
		if err := rows.Scan(&res, &file, &name); err != nil {
			t.Fatal(err)
		}
		if res != marker {
			t.Errorf("%s: resolution = %q, want %q", calleeName, res, marker)
		}
		if filepath.Base(file) != targetFile || name != targetName {
			t.Errorf("%s: resolved to %s:%s, want %s:%s", calleeName, file, name, targetFile, targetName)
		}
		got++
	}
	if got != wantN {
		t.Errorf("%s: %d resolved call(s), want %d", calleeName, got, wantN)
	}
}
