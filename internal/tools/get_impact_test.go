package tools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/mcp"
)

// setupRepoWithIndex creates a temp git repo, a small two-file Go project,
// indexes a synthetic call graph directly into the DB, then dirties one file.
// Returns: project root, *db.SQLiteStore, cleanup.
func setupRepoWithIndex(t *testing.T) (string, *db.SQLiteStore, func()) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()

	gitRun := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitRun("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\nfunc Foo(){}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun("add", "a.go")
	gitRun("commit", "-q", "-m", "init")

	dbPath := filepath.Join(root, ".max-context", "index.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		t.Fatalf("PrepareQueries: %v", err)
	}

	// Synthetic call graph:
	//   Foo (a.go) is called by Bar (b.go) and Baz (b.go).
	//   Bar is called by Top (c.go).
	res1, _ := q.InsertFunction.Exec("Foo", "a.go", 1, 5, "go", 1, "", "", "Foo()")
	foo, _ := res1.LastInsertId()
	res2, _ := q.InsertFunction.Exec("Bar", "b.go", 1, 5, "go", 1, "", "", "Bar()")
	bar, _ := res2.LastInsertId()
	res3, _ := q.InsertFunction.Exec("Baz", "b.go", 10, 15, "go", 1, "", "", "Baz()")
	baz, _ := res3.LastInsertId()
	res4, _ := q.InsertFunction.Exec("Top", "c.go", 1, 5, "go", 1, "", "", "Top()")
	top, _ := res4.LastInsertId()
	_, _ = q.InsertCall.Exec(bar, foo, "Foo", "b.go", 3)
	_, _ = q.InsertCall.Exec(baz, foo, "Foo", "b.go", 12)
	_, _ = q.InsertCall.Exec(top, bar, "Bar", "c.go", 3)

	// Dirty a.go in working tree (uncommitted).
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\nfunc Foo(){}\n// edit\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cleanup := func() {
		_ = q.Close()
		_ = database.Close()
	}
	return root, db.NewSQLiteStore(database), cleanup
}

func TestGetImpact_FromHEAD_DefaultCallers(t *testing.T) {
	root, store, cleanup := setupRepoWithIndex(t)
	defer cleanup()

	h := GetImpactHandler(store, root)
	resp, err := h(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	items := resp.([]mcp.ContentItem)
	if len(items) != 1 || items[0].Type != "text" {
		t.Fatalf("unexpected response shape: %+v", resp)
	}
	var out struct {
		Changed []struct {
			File    string   `json:"file"`
			Symbols []string `json:"symbols"`
		} `json:"changed"`
		Impacted []struct {
			File   string `json:"file"`
			Symbol string `json:"symbol"`
			Depth  int    `json:"depth"`
		} `json:"impacted"`
		Stats struct {
			ChangedFiles    int  `json:"changed_files"`
			ChangedSymbols  int  `json:"changed_symbols"`
			ImpactedSymbols int  `json:"impacted_symbols"`
			Truncated       bool `json:"truncated"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(items[0].Text), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Stats.ChangedFiles != 1 || out.Stats.ChangedSymbols != 1 {
		t.Fatalf("changed counts wrong: %+v", out.Stats)
	}
	// Expect Bar and Baz at depth=1 (callers of Foo), Top at depth=2 (caller of Bar).
	names := map[string]int{}
	for _, im := range out.Impacted {
		names[im.Symbol] = im.Depth
	}
	if names["Bar"] != 1 || names["Baz"] != 1 {
		t.Fatalf("missing depth-1 callers: %+v", names)
	}
	if names["Top"] != 2 {
		t.Fatalf("missing depth-2 caller Top: %+v", names)
	}
}

// TestGetImpact_ResolutionFieldsAndFilter verifies via_resolution, the stats
// resolution_breakdown, and that min_confidence prunes low-confidence edges.
func TestGetImpact_ResolutionFieldsAndFilter(t *testing.T) {
	root, store, cleanup := setupRepoWithIndex(t)
	defer cleanup()

	// Tag the seeded edges with distinct resolutions:
	//   Bar->Foo : receiver-typed (high confidence)
	//   Baz->Foo : name-global    (low confidence)
	db := store.DB()
	if _, err := db.Exec(`UPDATE calls SET resolution='receiver-typed' WHERE callee_name='Foo' AND file_path='b.go' AND line=3`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE calls SET resolution='name-global' WHERE callee_name='Foo' AND file_path='b.go' AND line=12`); err != nil {
		t.Fatal(err)
	}

	type impactOut struct {
		Impacted []struct {
			Symbol        string `json:"symbol"`
			ViaResolution string `json:"via_resolution"`
		} `json:"impacted"`
		Stats struct {
			ResolutionBreakdown map[string]int `json:"resolution_breakdown"`
		} `json:"stats"`
	}
	run := func(args string) impactOut {
		h := GetImpactHandler(store, root)
		resp, err := h(json.RawMessage(args))
		if err != nil {
			t.Fatalf("handler(%s): %v", args, err)
		}
		var out impactOut
		if err := json.Unmarshal([]byte(resp.([]mcp.ContentItem)[0].Text), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return out
	}

	// Depth 1 only, so the two direct callers of Foo are the population.
	all := run(`{"depth":1}`)
	byName := map[string]string{}
	for _, im := range all.Impacted {
		byName[im.Symbol] = im.ViaResolution
	}
	if byName["Bar"] != "receiver-typed" {
		t.Errorf("Bar via_resolution = %q, want receiver-typed", byName["Bar"])
	}
	if byName["Baz"] != "name-global" {
		t.Errorf("Baz via_resolution = %q, want name-global", byName["Baz"])
	}
	if all.Stats.ResolutionBreakdown["receiver-typed"] != 1 || all.Stats.ResolutionBreakdown["name-global"] != 1 {
		t.Errorf("breakdown = %+v, want one each", all.Stats.ResolutionBreakdown)
	}

	// min_confidence=receiver-typed prunes the name-global Baz edge.
	filtered := run(`{"depth":1,"min_confidence":"receiver-typed"}`)
	for _, im := range filtered.Impacted {
		if im.Symbol == "Baz" {
			t.Errorf("Baz should be pruned by min_confidence=receiver-typed")
		}
	}
	if len(filtered.Impacted) != 1 || filtered.Impacted[0].Symbol != "Bar" {
		t.Errorf("filtered impacted = %+v, want only Bar", filtered.Impacted)
	}
}

func TestGetImpact_NoChanges_EmptyResult(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	gitRun := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		_ = cmd.Run()
	}
	gitRun("init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0644)
	gitRun("add", "a.go")
	gitRun("commit", "-q", "-m", "init")

	database, _ := db.Open(filepath.Join(root, ".max-context/index.db"))
	defer database.Close()
	_ = db.Migrate(database)
	store := db.NewSQLiteStore(database)

	h := GetImpactHandler(store, root)
	resp, err := h(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	items := resp.([]mcp.ContentItem)
	var out struct {
		Stats struct {
			ChangedSymbols int `json:"changed_symbols"`
		} `json:"stats"`
	}
	_ = json.Unmarshal([]byte(items[0].Text), &out)
	if out.Stats.ChangedSymbols != 0 {
		t.Fatalf("expected 0 changed symbols on clean repo, got %d", out.Stats.ChangedSymbols)
	}
}
