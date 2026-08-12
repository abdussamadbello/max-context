package bench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRun_WritesResultsAndMarkdown(t *testing.T) {
	root := t.TempDir()
	// Tiny fixture file so grep has something to find.
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\nfunc Foo() {}\n"), 0644)

	questions := []Question{
		{ID: "q1", Text: "find Foo", Category: "lookup", Tool: "query_codebase", Terms: []string{"Foo"}},
	}
	out := t.TempDir()
	res, err := Run(root, questions, RunOptions{
		OutDir:     out,
		Repo:       "fixture",
		InvokeTool: func(string, json.RawMessage) (string, error) { return `{"answer":"Foo is at a.go:2"}`, nil },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Repo != "fixture" || len(res.Questions) != 1 {
		t.Fatalf("unexpected result shape: %+v", res)
	}
	if res.Questions[0].NaiveTokens <= 0 || res.Questions[0].SkilledTokens <= 0 {
		t.Fatalf("baselines should produce positive tokens, got %+v", res.Questions[0])
	}

	body, err := os.ReadFile(filepath.Join(out, "results.json"))
	if err != nil {
		t.Fatalf("read results.json: %v", err)
	}
	var parsed Results
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal results.json: %v", err)
	}
	if parsed.Repo != "fixture" {
		t.Fatalf("unexpected parsed.Repo: %s", parsed.Repo)
	}
}
