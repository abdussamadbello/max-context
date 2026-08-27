package grade

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/maxcontext/eval-locobench/internal/agent"
	"github.com/maxcontext/eval-locobench/internal/spec"
)

// RubricScore is the judge's per-item scoring of one edit answer.
type RubricScore struct {
	Items     map[string]int `json:"items"`     // rubric item id -> points awarded
	Total     int            `json:"total"`     // sum of awarded points
	MaxTotal  int            `json:"max_total"` // sum of max points
	Reasoning string         `json:"reasoning"` // judge's brief justification
}

// Judge scores an open-ended (edit) answer against a pre-registered rubric and a
// reference answer, using a DIFFERENT model than the one under test. The answer
// is presented BLIND — the judge never learns which arm produced it (the caller
// passes only the answer text; arm identity is stripped upstream).
type Judge struct {
	client agent.Caller
	model  string
}

func NewJudge(client agent.Caller, model string) *Judge {
	return &Judge{client: client, model: model}
}

const judgeSystem = "You are an impartial grader of an engineer's answer to a codebase task. " +
	"You are given the task, a reference answer (the standard), and a candidate answer. " +
	"Score the candidate ONLY against the provided rubric items. For each rubric item, award an integer " +
	"from 0 to its max_points. Be strict: award points only when the candidate clearly satisfies the criterion. " +
	"You do not know which tool or method produced the candidate; judge only its content. " +
	"Respond with ONLY a JSON object: {\"items\":{\"<id>\":<points>,...},\"reasoning\":\"<one sentence>\"}."

// Score grades one candidate answer. Returns the rubric score; on judge failure
// returns an error so the run log records it (no silent zero).
func (j *Judge) Score(ctx context.Context, task spec.Task, key spec.Key, candidate string) (*RubricScore, error) {
	maxTotal := 0
	var rubricLines []string
	for _, it := range task.Rubric {
		maxTotal += it.MaxPoints
		rubricLines = append(rubricLines, fmt.Sprintf("- id=%s (max %d): %s", it.ID, it.MaxPoints, it.Criterion))
	}

	prompt := fmt.Sprintf(
		"TASK:\n%s\n\nREFERENCE ANSWER (the standard):\n%s\n\nRUBRIC:\n%s\n\nCANDIDATE ANSWER:\n%s\n\n"+
			"Return ONLY the JSON object.",
		task.Intent, key.ReferenceAnswer, strings.Join(rubricLines, "\n"), candidate)

	out, err := j.client.Create(ctx, agent.CreateParams{
		Model: j.model, MaxTokens: 4096, Temperature: 0, System: judgeSystem,
		Messages: []agent.Message{{Role: "user", Content: []agent.ContentBlock{{Type: "text", Text: prompt}}}},
	})
	if err != nil {
		return nil, fmt.Errorf("judge call: %w", err)
	}
	if out.StopReason == "max_tokens" {
		return nil, fmt.Errorf("judge exhausted its output-token budget")
	}
	var text string
	for _, b := range out.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	parsed, err := parseJudgeJSON(text)
	if err != nil {
		return nil, fmt.Errorf("judge returned unparseable output: %w (raw: %q)", err, text)
	}

	score := &RubricScore{Items: map[string]int{}, MaxTotal: maxTotal, Reasoning: parsed.Reasoning}
	for _, it := range task.Rubric {
		pts := parsed.Items[it.ID]
		if pts < 0 {
			pts = 0
		}
		if pts > it.MaxPoints {
			pts = it.MaxPoints // clamp; judge can't exceed the pre-registered max
		}
		score.Items[it.ID] = pts
		score.Total += pts
	}
	return score, nil
}

type judgeJSON struct {
	Items     map[string]int `json:"items"`
	Reasoning string         `json:"reasoning"`
}

// parseJudgeJSON extracts the JSON object from the judge's reply, tolerating
// markdown fences or surrounding prose.
func parseJudgeJSON(s string) (*judgeJSON, error) {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object found")
	}
	var j judgeJSON
	if err := json.Unmarshal([]byte(s[start:end+1]), &j); err != nil {
		return nil, err
	}
	if j.Items == nil {
		j.Items = map[string]int{}
	}
	return &j, nil
}
