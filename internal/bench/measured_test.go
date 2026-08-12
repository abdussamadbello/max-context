package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The regression this guards: the max-context column used to be a hand-written
// integer in the question set while only the baselines were computed. The
// published ratio therefore moved when the repo grew and never when the tool
// output changed, and had drifted far enough to understate get_call_chain by
// up to 67x. Both sides are measured now, and the runner must refuse to run
// without a way to measure.
func TestRunRequiresAToolInvoker(t *testing.T) {
	_, err := Run(t.TempDir(), []Question{{ID: "q1", Tool: "query_codebase"}}, RunOptions{
		OutDir: t.TempDir(),
		Repo:   "x",
	})
	if err == nil {
		t.Fatal("Run succeeded with no InvokeTool; the max-context side would be unmeasured")
	}
	if !strings.Contains(err.Error(), "InvokeTool") {
		t.Errorf("error should name the missing invoker, got: %v", err)
	}
}

// The measured value must come from the response, so a bigger response has to
// produce a bigger number.
func TestMCTokensComeFromTheResponse(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root)

	short := runWith(t, root, func(string, json.RawMessage) (string, error) {
		return `{"answer":"short"}`, nil
	})
	long := runWith(t, root, func(string, json.RawMessage) (string, error) {
		return `{"answer":"` + strings.Repeat("verbose ", 400) + `"}`, nil
	})

	if !(long.Summary.AvgMC > short.Summary.AvgMC) {
		t.Errorf("a longer response did not raise avg_mc_tokens: short=%.1f long=%.1f",
			short.Summary.AvgMC, long.Summary.AvgMC)
	}
	// And a bigger max-context response must lower the reported savings.
	if !(long.Summary.SkilledSavingsX < short.Summary.SkilledSavingsX) {
		t.Errorf("a longer response did not lower the savings ratio: short=%.1fx long=%.1fx",
			short.Summary.SkilledSavingsX, long.Summary.SkilledSavingsX)
	}
}

// The tool and args recorded per question are what get invoked.
func TestRunInvokesEachQuestionsToolAndArgs(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root)

	var seen []string
	_ = runQuestions(t, root, []Question{
		{ID: "a", Tool: "get_definition", Terms: []string{"Alpha"}, MCArgs: json.RawMessage(`{"symbol":"Alpha"}`)},
		{ID: "b", Tool: "get_impact", Terms: []string{"Alpha"}, MCArgs: json.RawMessage(`{"files":["a.go"]}`)},
	}, func(tool string, args json.RawMessage) (string, error) {
		seen = append(seen, tool+" "+string(args))
		return `{"ok":true}`, nil
	})

	want := []string{`get_definition {"symbol":"Alpha"}`, `get_impact {"files":["a.go"]}`}
	if len(seen) != len(want) {
		t.Fatalf("invoked %d tools, want %d: %v", len(seen), len(want), seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, seen[i], want[i])
		}
	}
}

// An empty response means the question or its args went stale against the
// index. Silently scoring it as ~0 tokens would inflate the ratio without limit.
func TestEmptyResponseIsAnError(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root)

	_, err := Run(root, []Question{{ID: "q1", Tool: "query_codebase", Terms: []string{"Alpha"}}}, RunOptions{
		OutDir:     t.TempDir(),
		Repo:       "x",
		InvokeTool: func(string, json.RawMessage) (string, error) { return "", nil },
	})
	if err == nil {
		t.Fatal("an empty tool response was accepted; it would score as free")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Errorf("error should point at a stale question, got: %v", err)
	}
}

// A tool failure must fail the run rather than be scored.
func TestToolErrorFailsTheRun(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root)

	_, err := Run(root, []Question{{ID: "q1", Tool: "query_codebase", Terms: []string{"Alpha"}}}, RunOptions{
		OutDir:     t.TempDir(),
		Repo:       "x",
		InvokeTool: func(string, json.RawMessage) (string, error) { return "", fmt.Errorf("index not ready") },
	})
	if err == nil {
		t.Fatal("a failing tool call did not fail the run")
	}
	if !strings.Contains(err.Error(), "index not ready") {
		t.Errorf("error should carry the tool failure, got: %v", err)
	}
}

func writeFixture(t *testing.T, root string) {
	t.Helper()
	body := "package main\n\nfunc Alpha() {}\n\nfunc Beta() { Alpha() }\n"
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func runWith(t *testing.T, root string, invoke func(string, json.RawMessage) (string, error)) *Results {
	t.Helper()
	return runQuestions(t, root, []Question{
		{ID: "q1", Text: "find Alpha", Category: "lookup", Tool: "query_codebase", Terms: []string{"Alpha"}},
	}, invoke)
}

func runQuestions(t *testing.T, root string, qs []Question, invoke func(string, json.RawMessage) (string, error)) *Results {
	t.Helper()
	res, err := Run(root, qs, RunOptions{OutDir: t.TempDir(), Repo: "fixture", InvokeTool: invoke})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}
