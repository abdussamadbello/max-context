package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
)

// openIndexed opens the prepared database for a fixture project.
func openIndexed(t *testing.T, dbPath string) (*db.Queries, func()) {
	t.Helper()
	database, err := db.Open(dbPath)
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
	return q, func() { q.Close(); database.Close() }
}

// A context question is only meaningful alongside the budget it was measured
// at, since the compiler spends whatever budget it is given. Defaulting a
// missing budget would publish a number whose meaning depends on a value the
// question never stated.
func TestCompileContextForBenchRequiresTaskAndBudget(t *testing.T) {
	cfg := indexedProject(t)
	q, cleanup := openIndexed(t, cfg.DBPath)
	defer cleanup()
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	for name, tc := range map[string]struct {
		args string
		want string
	}{
		"no task":       {`{"budget":1000}`, "task"},
		"blank task":    {`{"task":"   ","budget":1000}`, "task"},
		"no budget":     {`{"task":"find the greeting"}`, "budget"},
		"zero budget":   {`{"task":"find the greeting","budget":0}`, "budget"},
		"malformed":     {`{"task":`, "parse context mc_args"},
		"empty payload": {``, "task"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := compileContextForBench(cfg, database, q, json.RawMessage(tc.args))
			if err == nil {
				t.Fatalf("expected an error for %s", name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// The benchmark measures `context` through a path that bypasses the MCP
// handler, because the command is deliberately unregistered. That path must
// still produce a real package the runner can tokenize.
func TestCompileContextForBenchReturnsBudgetedPackage(t *testing.T) {
	cfg := indexedProject(t)
	q, cleanup := openIndexed(t, cfg.DBPath)
	defer cleanup()
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	out, err := compileContextForBench(cfg, database, q,
		json.RawMessage(`{"task":"understand how greeting helpers work","budget":1500}`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("empty package; the benchmark would score this question as free")
	}
	var pkg struct {
		Tokenizer   string `json:"tokenizer"`
		TokenBudget int    `json:"token_budget"`
		TokensUsed  int    `json:"tokens_used"`
	}
	if err := json.Unmarshal([]byte(out), &pkg); err != nil {
		t.Fatalf("package is not valid JSON: %v", err)
	}
	if pkg.TokenBudget != 1500 {
		t.Errorf("token_budget = %d, want the requested 1500", pkg.TokenBudget)
	}
	if pkg.TokensUsed > pkg.TokenBudget {
		t.Errorf("tokens_used %d exceeds budget %d; the hard budget is not hard", pkg.TokensUsed, pkg.TokenBudget)
	}
	if pkg.Tokenizer == "" {
		t.Error("package does not name its tokenizer; the budget would be uninterpretable")
	}
}
