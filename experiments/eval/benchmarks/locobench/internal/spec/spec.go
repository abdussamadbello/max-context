// Package spec defines the pre-registered experiment protocol: repos (pinned
// SHAs), tasks (authored from user intent, NOT tool-shaped), and answer keys
// (built from an INDEPENDENT oracle, never from max-context output).
package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Repo is a codebase under test, pinned at a SHA for reproducibility.
type Repo struct {
	Name     string `json:"name"`      // e.g. "httpx"
	CloneURL string `json:"clone_url"` // git URL
	SHA      string `json:"sha"`       // pinned commit
	Lang     string `json:"lang"`      // "python" | "typescript"
}

// TaskType selects the grading mode.
type TaskType string

const (
	// Retrieval: objectively checkable — which file/symbol defines X, or the
	// exact caller set of F. Graded P/R/F1 vs a hand-verified key. No LLM judge.
	TypeRetrieval TaskType = "retrieval"
	// Impact: enumerable true-dependent set at a fixed depth/direction. Graded
	// objectively (set P/R) + judge for the qualitative remainder.
	TypeImpact TaskType = "impact"
	// Edit: open-ended ("where/how to add X"). Judge-scored against an atomic rubric.
	TypeEdit TaskType = "edit"
)

// Task is one experiment task. Authored from USER INTENT — it must NOT name a
// max-context tool or pre-supply search terms (that would bias the arms).
type Task struct {
	ID         string   `json:"id"`
	Repo       string   `json:"repo"`
	Type       TaskType `json:"type"`
	Intent     string   `json:"intent"`      // the user-facing task text (primary phrasing)
	Paraphrases []string `json:"paraphrases"` // ≥2 alternate phrasings; reduce prompt-sensitivity
	// PlantedGrepWin marks tasks expected to favor grep (prose/"why"/dynamic
	// dispatch). Reporting these honestly maps the boundary of where MC helps.
	PlantedGrepWin bool `json:"planted_grep_win,omitempty"`
	// Rubric items (edit tasks only): atomic, binary/ordinal checklist.
	Rubric []RubricItem `json:"rubric,omitempty"`
}

// RubricItem is one atomic, pre-registered grading criterion for edit tasks.
type RubricItem struct {
	ID       string `json:"id"`
	Criterion string `json:"criterion"` // e.g. "names the correct primary file"
	MaxPoints int    `json:"max_points"` // 1 for binary, 2 for ordinal
}

// Key is the answer key for one task, built from an INDEPENDENT oracle and
// hand-verified. For retrieval/impact it lists the gold file/symbol set.
type Key struct {
	TaskID string `json:"task_id"`
	// ExpectedFiles / ExpectedSymbols: the gold set the answer is graded against.
	ExpectedFiles   []string `json:"expected_files,omitempty"`
	ExpectedSymbols []string `json:"expected_symbols,omitempty"`
	// Provenance — how the key was built (audit trail).
	Oracle        string `json:"oracle"`         // e.g. "pyright find-references + hand-verified"
	OracleVersion string `json:"oracle_version"` // tool version
	HumanVerified bool   `json:"human_verified"`
	Partial       bool   `json:"partial,omitempty"` // dynamic dispatch: key is best-effort
	Notes         string `json:"notes,omitempty"`
	// ReferenceAnswer (edit tasks): the standard the judge scores against.
	ReferenceAnswer string `json:"reference_answer,omitempty"`
}

// Protocol is the full pre-registered experiment, hashed before any run.
type Protocol struct {
	Version       string `json:"version"`
	Repos         []Repo `json:"repos"`
	Tasks         []Task `json:"tasks"`
	Keys          []Key  `json:"keys"`
	TaskModel     string `json:"task_model"`     // model under test
	JudgeModel    string `json:"judge_model"`    // DIFFERENT model for judging
	Replicates    int    `json:"replicates"`     // k runs per (task,arm)
	MaxTurns      int    `json:"max_turns"`
	TokenBudget   int    `json:"token_budget"`
	SystemSkeleton string `json:"system_skeleton"` // shared prompt skeleton (both arms)
}

// Load reads a protocol JSON file.
func Load(path string) (*Protocol, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Protocol
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("parse protocol %s: %w", path, err)
	}
	return &p, nil
}

// KeyFor returns the key for a task id.
func (p *Protocol) KeyFor(taskID string) (Key, bool) {
	for _, k := range p.Keys {
		if k.TaskID == taskID {
			return k, true
		}
	}
	return Key{}, false
}

// Hash returns a stable SHA-256 of the protocol's grading-relevant content
// (tasks + keys + models), so pre-registration can be proven: the hash is
// committed before running, and recomputed after to show nothing changed.
func (p *Protocol) Hash() string {
	// Marshal a canonicalized subset with sorted task/key order.
	tasks := append([]Task(nil), p.Tasks...)
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	keys := append([]Key(nil), p.Keys...)
	sort.Slice(keys, func(i, j int) bool { return keys[i].TaskID < keys[j].TaskID })
	canon := struct {
		Version    string `json:"version"`
		Tasks      []Task `json:"tasks"`
		Keys       []Key  `json:"keys"`
		TaskModel  string `json:"task_model"`
		JudgeModel string `json:"judge_model"`
	}{p.Version, tasks, keys, p.TaskModel, p.JudgeModel}
	b, _ := json.Marshal(canon)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
