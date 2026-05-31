package spec

import (
	"path/filepath"
	"testing"
)

func TestLoadHttpxProtocol(t *testing.T) {
	p, err := Load(filepath.Join("..", "..", "protocol", "httpx.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(p.Repos) != 1 || p.Repos[0].Name != "httpx" {
		t.Fatalf("repos = %+v", p.Repos)
	}
	if p.Repos[0].SHA == "" {
		t.Error("repo SHA must be pinned")
	}
	// Every task must have a key; tasks must not leak tool names.
	for _, task := range p.Tasks {
		if _, ok := p.KeyFor(task.ID); !ok {
			t.Errorf("task %s has no key", task.ID)
		}
		if len(task.Paraphrases) < 2 {
			t.Errorf("task %s needs >=2 paraphrases (got %d)", task.ID, len(task.Paraphrases))
		}
		for _, banned := range []string{"query_codebase", "get_call_chain", "get_impact", "get_architecture", "grep", "ripgrep"} {
			if containsCI(task.Intent, banned) {
				t.Errorf("task %s intent leaks tool name %q (selection bias)", task.ID, banned)
			}
		}
	}
	// Models must differ (no self-judging).
	if p.TaskModel == p.JudgeModel {
		t.Errorf("judge model must differ from task model (got both %q)", p.TaskModel)
	}
	// Edit tasks must carry a rubric + reference answer.
	for _, task := range p.Tasks {
		if task.Type == TypeEdit {
			if len(task.Rubric) == 0 {
				t.Errorf("edit task %s missing rubric", task.ID)
			}
			k, _ := p.KeyFor(task.ID)
			if k.ReferenceAnswer == "" {
				t.Errorf("edit task %s missing reference answer", task.ID)
			}
		}
	}
}

func TestProtocolHashStable(t *testing.T) {
	p, err := Load(filepath.Join("..", "..", "protocol", "httpx.json"))
	if err != nil {
		t.Fatal(err)
	}
	h1 := p.Hash()
	h2 := p.Hash()
	if h1 != h2 {
		t.Errorf("hash not stable: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("hash not sha256 hex: %q", h1)
	}
	t.Logf("protocol hash (pre-registration): %s", h1)
}

func containsCI(haystack, needle string) bool {
	h := []rune(haystack)
	n := []rune(needle)
	for i := 0; i+len(n) <= len(h); i++ {
		match := true
		for j := range n {
			a, b := h[i+j], n[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
