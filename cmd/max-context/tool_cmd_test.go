package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxcontext/max-context/internal/config"
	"github.com/maxcontext/max-context/internal/contextpack"
	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/indexer"
)

// indexedProject builds a tiny indexed fixture project and returns its config.
func indexedProject(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte(
		"package app\n\nfunc Greet() string { return Name() }\n\nfunc Name() string { return \"x\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Fixture\n\ngreeting helpers\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root, &config.Flags{})
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(cfg.DBPath)
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
	if err := indexer.Index(context.Background(), root, database, q); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	return buf.String(), runErr
}

func TestRunDefCmd(t *testing.T) {
	cfg := indexedProject(t)
	out, err := captureStdout(t, func() error { return runDefCmd(cfg, []string{"Greet"}) })
	if err != nil {
		t.Fatalf("def: %v", err)
	}
	var payload struct {
		AnswerStatus string `json:"answer_status"`
		Matches      []struct {
			File string `json:"file"`
			Name string `json:"name"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %q", out)
	}
	if len(payload.Matches) == 0 || payload.Matches[0].Name != "Greet" || payload.Matches[0].File != "app.go" {
		t.Errorf("def output = %s", out)
	}
}

func TestRunQueryCmdDocsScope(t *testing.T) {
	cfg := indexedProject(t)
	out, err := captureStdout(t, func() error {
		return runQueryCmd(cfg, []string{"-scope", "docs", "greeting helpers"})
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	var payload struct {
		Results []struct {
			Kind string `json:"kind"`
			File string `json:"file"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %q", out)
	}
	if len(payload.Results) != 1 || payload.Results[0].Kind != "document" || payload.Results[0].File != "README.md" {
		t.Errorf("query -scope docs output = %s", out)
	}
}

func TestRunToolGenericAndErrors(t *testing.T) {
	cfg := indexedProject(t)
	out, err := captureStdout(t, func() error {
		return runToolGeneric(cfg, []string{"get_call_chain", "--json", `{"function_name":"Greet","direction":"callees"}`})
	})
	if err != nil {
		t.Fatalf("tool generic: %v", err)
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("stdout is not JSON: %q", out)
	}

	if err := runToolGeneric(cfg, []string{"get_call_chain", "--json", `{not json`}); err == nil {
		t.Error("invalid --json must error")
	}
	if err := runToolGeneric(cfg, []string{}); err == nil {
		t.Error("missing tool name must error")
	}
	if err := runTool(cfg, "no_such_tool", json.RawMessage(`{}`)); err == nil {
		t.Error("unknown tool must error")
	}
}

func TestRunImpactCmdBudget(t *testing.T) {
	cfg := indexedProject(t)
	out, err := captureStdout(t, func() error {
		return runImpactCmd(cfg, []string{"app.go", "-direction", "callees", "-budget", "500"})
	})
	if err != nil {
		t.Fatalf("impact: %v", err)
	}
	var payload struct {
		TokenBudget int `json:"token_budget"`
		TokensUsed  int `json:"tokens_used"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %q", out)
	}
	counter, _ := contextpack.NewCounter()
	actual, err := counter.Count(strings.TrimSpace(out))
	if err != nil {
		t.Fatal(err)
	}
	if payload.TokenBudget != 500 || payload.TokensUsed != actual || actual > payload.TokenBudget {
		t.Fatalf("budget metadata actual=%d payload=%+v", actual, payload)
	}

	if err := runImpactCmd(cfg, []string{"-budget", "-1", "app.go"}); err == nil {
		t.Fatal("negative budget must fail")
	}
}

func TestRunContextCmdProducesBudgetedPackage(t *testing.T) {
	cfg := indexedProject(t)
	out, err := captureStdout(t, func() error {
		return runContextCmd(cfg, []string{
			"change", "Greet", "behavior",
			"-intent", "change",
			"-changed-file", "app.go",
			"-budget", "800",
		})
	})
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	var payload struct {
		Intent      string `json:"intent"`
		TokenBudget int    `json:"token_budget"`
		TokensUsed  int    `json:"tokens_used"`
		Evidence    []struct {
			Symbol string `json:"symbol"`
			Kind   string `json:"kind"`
		} `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %q", out)
	}
	counter, _ := contextpack.NewCounter()
	actual, err := counter.Count(out)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Intent != "change" || payload.TokenBudget != 800 || payload.TokensUsed != actual || actual > 800 {
		t.Fatalf("invalid package metadata actual=%d payload=%+v", actual, payload)
	}
	foundGreet := false
	for _, item := range payload.Evidence {
		if item.Symbol == "Greet" {
			foundGreet = true
		}
	}
	if !foundGreet {
		t.Fatalf("task anchor missing from evidence: %s", out)
	}
}

func TestRunContextCmdValidation(t *testing.T) {
	cfg := indexedProject(t)
	if err := runContextCmd(cfg, nil); err == nil {
		t.Fatal("missing task must fail")
	}
	if err := runContextCmd(cfg, []string{"task", "-intent", "invent"}); err == nil {
		t.Fatal("invalid intent must fail")
	}
}
