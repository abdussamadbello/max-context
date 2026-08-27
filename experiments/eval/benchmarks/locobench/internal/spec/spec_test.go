package spec

import "testing"

// In the LoCoBench module the protocol is generated in-memory from scenarios
// (cmd/eval), never loaded from a pinned protocol JSON — so these tests exercise
// the harness invariants against a synthetic protocol rather than an httpx file.

func sampleProtocol() *Protocol {
	return &Protocol{
		Version:    "locobench-test",
		TaskModel:  "task-model",
		JudgeModel: "judge-model",
		Tasks: []Task{
			{
				ID:     "go_x_bug_investigation_easy_01",
				Type:   TypeEdit,
				Intent: "Find and explain the bug.",
				Rubric: []RubricItem{{ID: "c01", Criterion: "names culprit file", MaxPoints: 1}},
			},
		},
		Keys: []Key{
			{TaskID: "go_x_bug_investigation_easy_01", ReferenceAnswer: "the culprit is foo.go"},
		},
	}
}

func TestKeyForAndInvariants(t *testing.T) {
	p := sampleProtocol()

	// Every task must have a key, and edit tasks must carry rubric + reference.
	for _, task := range p.Tasks {
		k, ok := p.KeyFor(task.ID)
		if !ok {
			t.Errorf("task %s has no key", task.ID)
		}
		if task.Type == TypeEdit {
			if len(task.Rubric) == 0 {
				t.Errorf("edit task %s missing rubric", task.ID)
			}
			if k.ReferenceAnswer == "" {
				t.Errorf("edit task %s missing reference answer", task.ID)
			}
		}
	}

	// Models must differ (no self-judging).
	if p.TaskModel == p.JudgeModel {
		t.Errorf("judge model must differ from task model (got both %q)", p.TaskModel)
	}

	// KeyFor returns false for an unknown id.
	if _, ok := p.KeyFor("does-not-exist"); ok {
		t.Error("KeyFor should report missing for unknown task id")
	}
}

func TestProtocolHashStable(t *testing.T) {
	p := sampleProtocol()
	h1 := p.Hash()
	h2 := p.Hash()
	if h1 != h2 {
		t.Errorf("hash not stable: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("hash not sha256 hex: %q", h1)
	}

	// Hash must change when grading-relevant content changes (here, the rubric).
	p.Tasks[0].Rubric[0].Criterion = "different criterion"
	if p.Hash() == h1 {
		t.Error("hash should change when rubric criterion changes")
	}

	h2 = p.Hash()
	p.ContextBudget = 4000
	if p.Hash() == h2 {
		t.Error("hash should change when context budget changes")
	}

	h2 = p.Hash()
	p.Replicates++
	if p.Hash() == h2 {
		t.Error("hash should change when replicate count changes")
	}

	h2 = p.Hash()
	p.Repos = append(p.Repos, Repo{Name: "fixture", SHA: "abc123"})
	if p.Hash() == h2 {
		t.Error("hash should change when repository provenance changes")
	}
}
