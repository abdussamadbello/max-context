package contextcompiler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/artifacts"
	"github.com/maxcontext/max-context/internal/contextpack"
	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/indexer"
)

func compilerFixture(t *testing.T) (string, *db.Queries, *db.SQLiteStore, func()) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("auth/token.go", `package auth
func RefreshToken(expiry int) string { return GenerateToken(expiry) }
func GenerateToken(expiry int) string { return "token" }
`)
	write("auth/token_test.go", `package auth
func TestRefreshTokenExpiry() { _ = RefreshToken(60) }
`)
	write("docs/auth.md", "# Authentication\n\nRefresh token expiration is configured by the caller.\n")

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
	if err := indexer.Index(context.Background(), root, database, q); err != nil {
		t.Fatal(err)
	}
	artifactDir := filepath.Join(root, ".max-context")
	if err := artifacts.WriteSummary(artifactDir, database); err != nil {
		t.Fatal(err)
	}
	if err := artifacts.WriteArchitecture(artifactDir, database); err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		_ = q.Close()
		_ = database.Close()
	}
	return root, q, db.NewSQLiteStore(database), cleanup
}

func TestCompileProducesUsefulHardBudgetedPackage(t *testing.T) {
	root, q, store, cleanup := compilerFixture(t)
	defer cleanup()
	pkg, payload, err := Compile(context.Background(), store.DB(), q, root, Options{
		Task:         "Change RefreshToken expiration",
		TokenBudget:  1200,
		Intent:       IntentAuto,
		ChangedFiles: []string{"auth/token.go"},
		MaxDepth:     2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Intent != IntentChange {
		t.Fatalf("intent = %q, want change", pkg.Intent)
	}
	counter, _ := contextpack.NewCounter()
	actual, err := counter.Count(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	if actual != pkg.TokensUsed || actual > pkg.TokenBudget {
		t.Fatalf("actual=%d reported=%d budget=%d", actual, pkg.TokensUsed, pkg.TokenBudget)
	}
	foundAnchor, foundGraph, foundChange := false, false, false
	for _, item := range pkg.Evidence {
		foundAnchor = foundAnchor || item.Symbol == "RefreshToken"
		foundGraph = foundGraph || item.Kind == "call_graph"
		foundChange = foundChange || item.Kind == "changed_file"
	}
	if !foundAnchor || !foundGraph || !foundChange {
		t.Fatalf("package lacks routed evidence anchor=%v graph=%v change=%v: %+v", foundAnchor, foundGraph, foundChange, pkg.Evidence)
	}
}

func TestCompileExplicitLocateSkipsBroaderLanes(t *testing.T) {
	root, q, store, cleanup := compilerFixture(t)
	defer cleanup()
	pkg, _, err := Compile(context.Background(), store.DB(), q, root, Options{
		Task:        "RefreshToken",
		TokenBudget: 800,
		Intent:      IntentLocate,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range pkg.Evidence {
		if item.Kind == "call_graph" || item.Kind == "impact" || item.Kind == "changed_file" || item.Kind == "architecture" {
			t.Fatalf("locate intent included broad evidence: %+v", item)
		}
	}
}

func TestCompileValidationAndCancellation(t *testing.T) {
	if _, err := resolveIntent("task", "invent"); err == nil {
		t.Fatal("invalid intent must fail")
	}
	counter, _ := contextpack.NewCounter()
	_, _, err := contextpack.Pack(counter, "task", "change", 1, nil, nil)
	if !errors.Is(err, contextpack.ErrBudgetTooSmall) {
		t.Fatalf("small-budget error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = Compile(ctx, nil, nil, "", Options{Task: "task", TokenBudget: 100})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled compile error = %v", err)
	}
}

func TestRetrievalQueriesPreferConcreteTaskTerms(t *testing.T) {
	queries := retrievalQueries("Perform an initial security audit of authentication secrets, configuration, and server hardening. Locate the relevant code.")
	if len(queries) < 6 {
		t.Fatalf("queries = %v", queries)
	}
	want := map[string]bool{"auth": false, "jwt": false, "secret": false, "config": false, "server": false, "timeout": false}
	for _, query := range queries[1:] {
		if _, ok := want[query]; ok {
			want[query] = true
		}
	}
	for term, found := range want {
		if !found {
			t.Errorf("concrete term %q missing from queries %v", term, queries)
		}
	}
	for _, query := range queries[1:] {
		if query == "configuration" || query == "hardening" || query == "authentication" {
			t.Errorf("query %q duplicates its normalized alias: %v", query, queries)
		}
	}
}
