package scenario

import "testing"

func TestProjectPrefix(t *testing.T) {
	cases := []struct {
		id, cat, diff, want string
	}{
		{"go_api_graphql_hard_007_feature_implementation_medium_01", "feature_implementation", "medium", "go_api_graphql_hard_007"},
		{"typescript_blockchain_nft_expert_035_feature_implementation_hard_01", "feature_implementation", "hard", "typescript_blockchain_nft_expert_035"},
		{"php_ml_nlp_expert_017_security_analysis_easy_01", "security_analysis", "easy", "php_ml_nlp_expert_017"},
		{"csharp_system_networking_easy_063_multi_session_development_hard_01", "multi_session_development", "hard", "csharp_system_networking_easy_063"},
	}
	for _, c := range cases {
		s := &Scenario{ID: c.id, TaskCategory: c.cat, Difficulty: c.diff}
		got, err := s.ProjectPrefix()
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.id, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: prefix = %q, want %q", c.id, got, c.want)
		}
	}
}

func TestToTaskRubric(t *testing.T) {
	s := &Scenario{
		ID:                 "go_api_graphql_hard_007_feature_implementation_medium_01",
		TaskCategory:       "feature_implementation",
		Difficulty:         "medium",
		Title:              "Implement Publish",
		Description:        "desc",
		GroundTruth: flexText{raw: []byte(`"the truth"`)},
		EvaluationCriteria: []flexCriterion{
			{raw: []byte(`"crit one"`)}, {raw: []byte(`"crit two"`)}, {raw: []byte(`"crit three"`)},
		},
	}
	task, key := ToTask(s, IntentDiscovery)
	if task.Type != "edit" {
		t.Errorf("task type = %q, want edit", task.Type)
	}
	if len(task.Rubric) != 3 {
		t.Fatalf("rubric len = %d, want 3", len(task.Rubric))
	}
	if task.Rubric[0].ID != "c01" || task.Rubric[0].MaxPoints != 1 {
		t.Errorf("rubric[0] = %+v, want id=c01 max=1", task.Rubric[0])
	}
	if key.ReferenceAnswer != "the truth" {
		t.Errorf("reference answer = %q", key.ReferenceAnswer)
	}
	// Discovery mode must NOT leak the step-by-step file recipe.
	if task.Intent == "" {
		t.Error("intent is empty")
	}
}

func TestFlexText(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"plain string", `"hello world"`, "hello world"},
		{"object", `{"vulnerability":"SQLi","solution":"use params"}`, "SOLUTION: use params\nVULNERABILITY: SQLi"},
		{"array", `["one","two"]`, "one\ntwo"},
		{"empty", ``, ""},
	}
	for _, c := range cases {
		var f flexText
		if c.raw != "" {
			f = flexText{raw: []byte(c.raw)}
		}
		if got := f.String(); got != c.want {
			t.Errorf("%s: String() = %q, want %q", c.name, got, c.want)
		}
	}
}
