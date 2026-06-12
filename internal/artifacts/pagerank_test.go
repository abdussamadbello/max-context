package artifacts

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
)

func rankTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "rank.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	return database
}

func seedFunc(t *testing.T, database *sql.DB, name, file string) int64 {
	t.Helper()
	res, err := database.Exec(
		`INSERT INTO functions (name, file_path, start_line, end_line, language) VALUES (?, ?, 1, 5, 'go')`,
		name, file)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func seedCall(t *testing.T, database *sql.DB, caller, callee int64, resolution string) {
	t.Helper()
	if _, err := database.Exec(
		`INSERT INTO calls (caller_id, callee_id, callee_name, file_path, line, resolution) VALUES (?, ?, 'x', 'f.go', 1, ?)`,
		caller, callee, resolution); err != nil {
		t.Fatal(err)
	}
}

// TestPageRankConvergenceTarget: with A->B and C->B, B must rank highest.
func TestPageRankConvergenceTarget(t *testing.T) {
	database := rankTestDB(t)
	a := seedFunc(t, database, "A", "a.go")
	b := seedFunc(t, database, "B", "b.go")
	c := seedFunc(t, database, "C", "c.go")
	seedCall(t, database, a, b, "same-package")
	seedCall(t, database, c, b, "same-package")

	ranked, err := PageRankFunctions(database)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) != 3 {
		t.Fatalf("ranked %d funcs, want 3", len(ranked))
	}
	if ranked[0].Name != "B" {
		t.Errorf("top ranked = %s, want B (ranking: %+v)", ranked[0].Name, ranked)
	}
	if ranked[0].Score <= ranked[1].Score {
		t.Errorf("B's score must strictly exceed the others: %+v", ranked)
	}

	scores := FileScores(ranked)
	if scores["b.go"] <= scores["a.go"] {
		t.Errorf("file scores must follow function scores: %+v", scores)
	}
	_ = c
}

// TestPageRankExcludesInterfaceDispatch: low-confidence fan-out edges must not
// boost a target.
func TestPageRankExcludesInterfaceDispatch(t *testing.T) {
	database := rankTestDB(t)
	a := seedFunc(t, database, "A", "a.go")
	b := seedFunc(t, database, "B", "b.go")
	c := seedFunc(t, database, "C", "c.go")
	// Only interface-dispatch edges point at B; a resolved edge points at C.
	seedCall(t, database, a, b, "interface-dispatch")
	seedCall(t, database, a, c, "same-package")

	ranked, err := PageRankFunctions(database)
	if err != nil {
		t.Fatal(err)
	}
	if ranked[0].Name != "C" {
		t.Errorf("top ranked = %s, want C (interface-dispatch must be excluded): %+v", ranked[0].Name, ranked)
	}
}

// TestWriteArchitectureDeterministic: two runs over the same database produce
// byte-identical output (the old map-iteration directory section did not).
func TestWriteArchitectureDeterministic(t *testing.T) {
	database := rankTestDB(t)
	a := seedFunc(t, database, "A", "internal/a.go")
	b := seedFunc(t, database, "B", "pkg/b.go")
	c := seedFunc(t, database, "C", "cmd/c.go")
	seedCall(t, database, a, b, "same-package")
	seedCall(t, database, c, b, "same-package")
	for _, imp := range []struct{ from, to string }{
		{"pkg/b.go", "fmt"}, {"pkg/b.go", "os"}, {"internal/a.go", "strings"},
	} {
		if _, err := database.Exec(
			`INSERT INTO imports (file_path, imported_path) VALUES (?, ?)`, imp.from, imp.to); err != nil {
			t.Fatal(err)
		}
	}

	render := func(dir string) string {
		t.Helper()
		if err := WriteArchitecture(dir, database); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(filepath.Join(dir, "architecture.md"))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	first := render(t.TempDir())
	second := render(t.TempDir())
	if first != second {
		t.Errorf("architecture.md not deterministic:\n--- first\n%s\n--- second\n%s", first, second)
	}
	for _, want := range []string{"## Key functions (PageRank)", "- B (pkg/b.go)", "- pkg/b.go -> fmt"} {
		if !strings.Contains(first, want) {
			t.Errorf("architecture.md missing %q:\n%s", want, first)
		}
	}
}
