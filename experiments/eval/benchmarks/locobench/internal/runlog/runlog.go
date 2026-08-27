// Package runlog defines the persisted experiment records. Everything needed to
// audit or reproduce a run is captured; no run is silently dropped.
package runlog

import (
	"github.com/maxcontext/eval-locobench/internal/agent"
	"github.com/maxcontext/eval-locobench/internal/grade"
)

// Arm identifies which toolset produced a run.
type Arm string

const (
	ArmGrep       Arm = "grep"        // baseline: ripgrep + read_file + list_files
	ArmMaxContext Arm = "max-context" // mc-only mechanism probe: the 5 MCP tools over stdio
	ArmHybrid     Arm = "hybrid"      // realistic deployment: grep+read+list AND the 5 MCP tools
	ArmContext    Arm = "context"     // one token-budgeted package compiled from the task
)

// RunRecord is one (task, arm, replicate) execution, fully audit-able.
type RunRecord struct {
	TaskID       string          `json:"task_id"`
	Repo         string          `json:"repo"`
	Arm          Arm             `json:"arm"`
	Replicate    int             `json:"replicate"`
	Phrasing     string          `json:"phrasing"` // the exact task text used
	Status       agent.RunStatus `json:"status"`   // terminal status (no silent drop)
	FinalAnswer  string          `json:"final_answer"`
	InputTokens  int             `json:"input_tokens"`
	OutputTokens int             `json:"output_tokens"`
	ToolCalls    int             `json:"tool_calls"`
	Turns        int             `json:"turns_to_answer"`
	WallClockMS  int64           `json:"wall_clock_ms"`
	Retries      int             `json:"retries"`

	// Grading (filled per task type; mutually exclusive).
	Retrieval *grade.SetScore    `json:"retrieval,omitempty"`
	Rubric    *grade.RubricScore `json:"rubric,omitempty"`
	GradeErr  string             `json:"grade_error,omitempty"`

	// Transcript path (full request/response trace written separately).
	TranscriptFile string `json:"transcript_file"`
}

// Manifest captures the run's provenance so a third party can reproduce it.
type Manifest struct {
	ProtocolHash    string            `json:"protocol_hash"` // pre-registration proof
	ProtocolFile    string            `json:"protocol_file"`
	TaskModel       string            `json:"task_model"`
	JudgeModel      string            `json:"judge_model"`
	Temperature     float64           `json:"temperature"`
	MaxTurns        int               `json:"max_turns"`
	TokenBudget     int               `json:"token_budget"`
	ContextBudget   int               `json:"context_budget"`
	Replicates      int               `json:"replicates"`
	Backend         string            `json:"backend"`
	BaseURL         string            `json:"base_url,omitempty"`
	RepoSHAs        map[string]string `json:"repo_shas"`       // repo -> checked-out SHA
	IndexSHAs       map[string]string `json:"index_shas"`      // repo -> SHA max-context indexed (must match)
	MaxContextBin   string            `json:"max_context_bin"` // binary path/version
	RGVersion       string            `json:"rg_version"`
	StartedAt       string            `json:"started_at"`    // RFC3339 (passed in; no Date.now in core)
	GrepPlaybook    string            `json:"grep_playbook"` // verbatim, for audit
	MCPlaybook      string            `json:"mc_playbook"`
	ContextPlaybook string            `json:"context_playbook"`
}

// Results is the full machine-readable output.
type Results struct {
	Manifest Manifest    `json:"manifest"`
	Runs     []RunRecord `json:"runs"`
}
