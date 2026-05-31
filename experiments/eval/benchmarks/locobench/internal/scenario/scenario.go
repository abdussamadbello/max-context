// Package scenario loads LoCoBench evaluation scenarios from disk and adapts
// them to the shared experiment harness (internal/spec).
//
// LoCoBench (arXiv 2509.09614, Salesforce AI Research, Apache 2.0) ships 8,000
// scenarios over synthetic multi-file projects. As published it is a code-
// GENERATION+execution benchmark that feeds the whole codebase into the prompt.
// We repurpose it (see benchmarks/locobench/README.md "Option A"): index the
// project, give BOTH arms only the task and let each gather context its own way
// (max-context MCP vs grep+read), then grade the produced answer identically
// with the shared blind judge against the scenario's own ground_truth +
// evaluation_criteria. The measured variable stays the same as the in-house
// study: how the agent gathers context, not the model.
package scenario

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// flexText holds a scenario field that may be a JSON string, object, or array.
// It unmarshals any of them and renders a readable string via String(): plain
// strings pass through; objects/arrays are flattened to "KEY: value" lines (for
// objects) or newline-joined items (for arrays), recursively. This keeps the
// loader robust to LoCoBench's mixed task_prompt / ground_truth shapes.
type flexText struct {
	raw json.RawMessage
}

func (f *flexText) UnmarshalJSON(b []byte) error {
	f.raw = append(f.raw[:0], b...)
	return nil
}

func (f flexText) String() string {
	if len(f.raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(f.raw, &v); err != nil {
		return string(f.raw)
	}
	var b strings.Builder
	flatten(&b, v, "")
	return strings.TrimSpace(b.String())
}

func flatten(b *strings.Builder, v any, prefix string) {
	switch t := v.(type) {
	case string:
		b.WriteString(t)
		b.WriteByte('\n')
	case []any:
		for _, e := range t {
			flatten(b, e, prefix)
		}
	case map[string]any:
		// Stable key order for deterministic output (protocol hashing).
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(b, "%s%s: ", prefix, strings.ToUpper(k))
			// Inline scalar values; recurse into nested structures on a new line.
			switch t[k].(type) {
			case string:
				flatten(b, t[k], "")
			default:
				b.WriteByte('\n')
				flatten(b, t[k], prefix+"  ")
			}
		}
	default:
		fmt.Fprintf(b, "%v\n", t)
	}
}

// Scenario is one LoCoBench evaluation scenario, mirroring the on-disk JSON
// schema (data/output/scenarios/<id>.json).
//
// Note: task_prompt and ground_truth are NOT always strings. Multi-session and
// structured scenarios store them as objects (e.g. {session_1:…} or
// {vulnerability, culprit_files, …}) or arrays. We model them as flexText, which
// accepts any JSON shape and flattens to a readable string for the prompt/judge.
type Scenario struct {
	ID                 string         `json:"id"`
	TaskCategory       string         `json:"task_category"`
	Difficulty         string         `json:"difficulty"`
	Title              string         `json:"title"`
	Description        string         `json:"description"`
	ContextFiles       []string       `json:"context_files"` // project-relative, INCLUDE inner-project prefix
	ContextLength      int             `json:"context_length"`
	TaskPrompt         flexText        `json:"task_prompt"`
	ExpectedApproach   flexText        `json:"expected_approach"`
	GroundTruth        flexText        `json:"ground_truth"`
	EvaluationCriteria []flexCriterion `json:"evaluation_criteria"`
	Metadata           map[string]any  `json:"metadata"`
}

// flexCriterion is one evaluation-criteria entry. LoCoBench stores these either
// as a plain string or as a {criterion, description, scoring} object; both
// render to a single criterion line for the rubric.
type flexCriterion struct {
	raw json.RawMessage
}

func (c *flexCriterion) UnmarshalJSON(b []byte) error {
	c.raw = append(c.raw[:0], b...)
	return nil
}

func (c flexCriterion) String() string {
	if len(c.raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(c.raw, &s) == nil {
		return s
	}
	var obj struct {
		Criterion   string `json:"criterion"`
		Description string `json:"description"`
	}
	if json.Unmarshal(c.raw, &obj) == nil && obj.Criterion != "" {
		if obj.Description != "" {
			return obj.Criterion + " — " + obj.Description
		}
		return obj.Criterion
	}
	return string(c.raw)
}

// knownDifficulties are the difficulty tokens embedded in scenario ids. NOTE:
// the difficulty embedded in the id is the generation *target* and does not
// always equal the scenario's achieved `difficulty` field — so the prefix must
// be parsed from the id's own tokens, not by substituting the field.
var knownDifficulties = []string{"easy", "medium", "hard", "expert"}

// ProjectPrefix derives the data/generated/<prefix> directory name from the
// scenario id. The id is "{project_prefix}_{category}_{difficulty}_{NN}", e.g.
//
//	go_api_graphql_hard_007_feature_implementation_medium_01
//	└──────── prefix ───────┘└──── category ────┘└ diff ┘└NN┘
//
// We strip the known "_<category>_<difficulty>_<NN>" suffix. The category comes
// from the field, but the difficulty is matched against the known set (the id's
// difficulty may differ from the achieved `difficulty` field).
func (s *Scenario) ProjectPrefix() (string, error) {
	for _, diff := range knownDifficulties {
		suffix := "_" + s.TaskCategory + "_" + diff + "_"
		if i := strings.LastIndex(s.ID, suffix); i >= 0 {
			return s.ID[:i], nil
		}
	}
	return "", fmt.Errorf("scenario id %q does not contain a '_%s_<difficulty>_NN' suffix", s.ID, s.TaskCategory)
}

// RepoRoot returns the absolute path to index/search for this scenario: the
// inner synthetic-project directory under data/generated/<prefix>/. The inner
// dir (e.g. "CanvasCaster") is the real source root and holds the build file;
// context_files paths are relative to <prefix>/ and include that inner name.
//
// dataDir is the extracted LoCoBench data root (the dir holding generated/ and
// output/). Returns the project-prefix dir too, since context_files resolve
// against it (not the inner dir).
func (s *Scenario) RepoRoot(dataDir string) (projectDir, innerRoot string, err error) {
	prefix, err := s.ProjectPrefix()
	if err != nil {
		return "", "", err
	}
	projectDir = filepath.Join(dataDir, "generated", prefix)
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return "", "", fmt.Errorf("read project dir %s: %w", projectDir, err)
	}
	// The inner root is the sole subdirectory (the other entry is
	// project_metadata.json). Pick the first directory entry.
	for _, e := range entries {
		if e.IsDir() {
			return projectDir, filepath.Join(projectDir, e.Name()), nil
		}
	}
	return "", "", fmt.Errorf("no inner project directory under %s", projectDir)
}

// Load reads a single scenario JSON file.
func Load(path string) (*Scenario, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Scenario
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse scenario %s: %w", path, err)
	}
	if s.ID == "" {
		return nil, fmt.Errorf("scenario %s has empty id", path)
	}
	return &s, nil
}

// LoadDir loads every *.json scenario under scenariosDir, optionally filtered.
// A nil/empty filter loads all. Filtering is applied on the parsed scenario.
func LoadDir(scenariosDir string, keep func(*Scenario) bool) ([]*Scenario, error) {
	entries, err := os.ReadDir(scenariosDir)
	if err != nil {
		return nil, err
	}
	var out []*Scenario
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		s, err := Load(filepath.Join(scenariosDir, e.Name()))
		if err != nil {
			return nil, err
		}
		if keep == nil || keep(s) {
			out = append(out, s)
		}
	}
	return out, nil
}
