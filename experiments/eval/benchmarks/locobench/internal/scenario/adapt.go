package scenario

import (
	"fmt"
	"strings"

	"github.com/maxcontext/eval-locobench/internal/spec"
)

// ToTask converts a LoCoBench scenario into the harness's spec.Task + spec.Key.
//
// Every LoCoBench task is open-ended ("produce a solution / explain the change"),
// so it maps to spec.TypeEdit and is graded by the blind, different-model judge —
// exactly like the in-house edit tasks. The scenario's evaluation_criteria become
// the rubric (one binary point each); its ground_truth becomes the judge's
// reference answer. Grading is identical for both arms, so it never biases the
// max-context-vs-grep comparison.
//
// The Task.Intent is the prompt the agent actually receives. How much of the
// scenario we put in it is a real experimental-design choice (how much we let the
// prompt pre-supply vs. force the agent to discover) — see BuildIntent.
func ToTask(s *Scenario, mode IntentMode) (spec.Task, spec.Key) {
	rubric := make([]spec.RubricItem, 0, len(s.EvaluationCriteria))
	for i, c := range s.EvaluationCriteria {
		rubric = append(rubric, spec.RubricItem{
			ID:        fmt.Sprintf("c%02d", i+1),
			Criterion: c.String(),
			MaxPoints: 1, // binary: criterion satisfied or not
		})
	}

	task := spec.Task{
		ID:     s.ID,
		Repo:   mustPrefix(s),
		Type:   spec.TypeEdit,
		Intent: BuildIntent(s, mode),
		Rubric: rubric,
	}

	key := spec.Key{
		TaskID:          s.ID,
		Oracle:          "locobench-shipped (ground_truth + evaluation_criteria)",
		OracleVersion:   "2509.09614",
		HumanVerified:   false, // LoCoBench-generated, not independently re-verified by us
		ReferenceAnswer: s.GroundTruth.String(),
		Notes:           "task_category=" + s.TaskCategory + " difficulty=" + s.Difficulty,
	}
	return task, key
}

func mustPrefix(s *Scenario) string {
	p, err := s.ProjectPrefix()
	if err != nil {
		return s.ID // degrade gracefully; loader already validated id
	}
	return p
}

// IntentMode selects how much of the scenario is handed to the agent up front.
// This is the lever that determines what the experiment measures.
type IntentMode int

const (
	// IntentFull gives the scenario's own task_prompt verbatim. LoCoBench's
	// prompts often already name the files to edit, so this measures execution
	// quality more than navigation — closest to "run LoCoBench as written."
	IntentFull IntentMode = iota

	// IntentDiscovery gives only the high-level title + description + the
	// requirement, deliberately WITHOUT the step-by-step file list, forcing each
	// arm to locate the relevant code itself. This is the navigation-stressing
	// variant that best exercises max-context's value.
	IntentDiscovery
)

// BuildIntent assembles the user-facing task text from the scenario per mode.
// It never names a max-context tool or pre-supplies search terms beyond what the
// chosen mode includes (the in-house anti-bias rule).
func BuildIntent(s *Scenario, mode IntentMode) string {
	var b strings.Builder
	switch mode {
	case IntentDiscovery:
		// Title + description + ground-level ask, but strip the numbered
		// file-by-file recipe so the agent must find the code itself.
		fmt.Fprintf(&b, "%s\n\n%s\n\n", s.Title, s.Description)
		b.WriteString("Locate the relevant code in the repository and describe precisely " +
			"what to change to accomplish this — name the exact files and functions, and " +
			"explain the change in each. Cite exact repo-relative paths.")
	default: // IntentFull
		if s.Title != "" {
			fmt.Fprintf(&b, "%s\n\n", s.Title)
		}
		if s.Description != "" {
			fmt.Fprintf(&b, "%s\n\n", s.Description)
		}
		b.WriteString(s.TaskPrompt.String())
	}
	return b.String()
}
