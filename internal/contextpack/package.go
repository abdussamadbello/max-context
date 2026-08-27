package contextpack

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const TokenizerCL100K = "cl100k_base"

var evidenceKindCaps = map[string]int{
	"anchor_file":   6,
	"architecture":  1,
	"call_graph":    3,
	"changed_file":  8,
	"definition":    8,
	"documentation": 4,
	"file":          6,
	"impact":        10,
	"lexical":       8,
	"test":          6,
	"type":          6,
}

const defaultKindCap = 8
const maxEvidencePerFile = 3

type Evidence struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	File       string  `json:"file,omitempty"`
	Line       int     `json:"line,omitempty"`
	Symbol     string  `json:"symbol,omitempty"`
	Content    string  `json:"content"`
	Reason     string  `json:"reason"`
	Confidence string  `json:"confidence"`
	Relevance  float64 `json:"relevance"`
	TokenCost  int     `json:"token_cost"`

	// Priority is an explicit policy tier. It is used for ordering but omitted
	// from the output so an internal tuning knob does not become a public API.
	Priority int `json:"-"`
}

type Omitted struct {
	Total  int            `json:"total"`
	ByKind map[string]int `json:"by_kind"`
}

type Package struct {
	Task        string     `json:"task"`
	Intent      string     `json:"intent"`
	Tokenizer   string     `json:"tokenizer"`
	TokenBudget int        `json:"token_budget"`
	TokensUsed  int        `json:"tokens_used"`
	Complete    bool       `json:"complete"`
	Evidence    []Evidence `json:"evidence"`
	Omitted     Omitted    `json:"omitted"`
	Warnings    []string   `json:"warnings,omitempty"`
}

// Pack ranks, deduplicates, and greedily admits evidence while measuring the
// complete serialized package after every decision. Large skipped items do not
// prevent smaller later items from using the remaining budget.
func Pack(counter *Counter, task, intent string, budget int, candidates []Evidence, warnings []string) (Package, []byte, error) {
	if counter == nil {
		return Package{}, nil, fmt.Errorf("token counter is required")
	}
	if budget <= 0 {
		return Package{}, nil, fmt.Errorf("token budget must be positive")
	}

	unique := deduplicate(candidates)
	for i := range unique {
		cost, err := evidenceTokenCost(counter, unique[i])
		if err != nil {
			return Package{}, nil, err
		}
		unique[i].TokenCost = cost
	}
	sortEvidence(unique)

	omitted := Omitted{Total: len(unique), ByKind: map[string]int{}}
	for _, item := range unique {
		omitted.ByKind[item.Kind]++
	}
	pkg := Package{
		Task:        strings.TrimSpace(task),
		Intent:      strings.ToLower(strings.TrimSpace(intent)),
		Tokenizer:   TokenizerCL100K,
		TokenBudget: budget,
		Evidence:    []Evidence{},
		Omitted:     omitted,
		Warnings:    append([]string(nil), warnings...),
	}
	payload, tokens, err := stablePackageJSON(counter, pkg)
	if err != nil {
		return Package{}, nil, err
	}
	if tokens > budget {
		return Package{}, nil, fmt.Errorf("%w: need at least %d tokens, got %d", ErrBudgetTooSmall, tokens, budget)
	}
	pkg.TokensUsed = tokens
	selectedByKind := map[string]int{}
	selectedByFile := map[string]int{}

	for _, item := range unique {
		kindCap := evidenceKindCaps[item.Kind]
		if kindCap == 0 {
			kindCap = defaultKindCap
		}
		countFile := itemCountsTowardFileCap(item.Kind)
		if selectedByKind[item.Kind] >= kindCap || (countFile && item.File != "" && selectedByFile[item.File] >= maxEvidencePerFile) {
			continue
		}
		trial := pkg
		trial.Evidence = append(append([]Evidence(nil), pkg.Evidence...), item)
		trial.Omitted = cloneOmitted(pkg.Omitted)
		trial.Omitted.Total--
		trial.Omitted.ByKind[item.Kind]--
		if trial.Omitted.ByKind[item.Kind] == 0 {
			delete(trial.Omitted.ByKind, item.Kind)
		}
		trial.Complete = trial.Omitted.Total == 0
		trialJSON, trialTokens, err := stablePackageJSON(counter, trial)
		if err != nil {
			return Package{}, nil, err
		}
		if trialTokens <= budget {
			trial.TokensUsed = trialTokens
			pkg, payload = trial, trialJSON
			selectedByKind[item.Kind]++
			if countFile && item.File != "" {
				selectedByFile[item.File]++
			}
		}
	}

	// A skipped candidate remains omitted even if every later, smaller candidate
	// fit. Re-render once so complete and tokens_used describe the final package.
	pkg.Complete = pkg.Omitted.Total == 0
	payload, tokens, err = stablePackageJSON(counter, pkg)
	if err != nil {
		return Package{}, nil, err
	}
	pkg.TokensUsed = tokens
	return pkg, payload, nil
}

func itemCountsTowardFileCap(kind string) bool {
	switch kind {
	case "architecture", "call_graph", "changed_file":
		return false
	default:
		return true
	}
}

func stablePackageJSON(counter *Counter, pkg Package) ([]byte, int, error) {
	return renderWithStableCount(counter, 0, func(_ int, tokensUsed int) ([]byte, error) {
		pkg.TokensUsed = tokensUsed
		return json.Marshal(pkg)
	})
}

func evidenceTokenCost(counter *Counter, item Evidence) (int, error) {
	item.TokenCost = 0
	return counter.CountJSON(item)
}

func cloneOmitted(in Omitted) Omitted {
	out := Omitted{Total: in.Total, ByKind: make(map[string]int, len(in.ByKind))}
	for kind, count := range in.ByKind {
		out.ByKind[kind] = count
	}
	return out
}

func deduplicate(items []Evidence) []Evidence {
	positions := map[string]int{}
	out := make([]Evidence, 0, len(items))
	for i, item := range items {
		item.ID = strings.TrimSpace(item.ID)
		if item.ID == "" {
			item.ID = fmt.Sprintf("%s:%s:%d:%s:%d", item.Kind, item.File, item.Line, item.Symbol, i)
		}
		if pos, ok := positions[item.ID]; ok {
			prior := out[pos]
			if item.Priority > prior.Priority || (item.Priority == prior.Priority && item.Relevance > prior.Relevance) {
				out[pos] = item
			}
			continue
		}
		positions[item.ID] = len(out)
		out = append(out, item)
	}
	return out
}

func sortEvidence(items []Evidence) {
	confidence := map[string]int{"high": 3, "medium": 2, "low": 1}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		if a.Relevance != b.Relevance {
			return a.Relevance > b.Relevance
		}
		if confidence[a.Confidence] != confidence[b.Confidence] {
			return confidence[a.Confidence] > confidence[b.Confidence]
		}
		if a.TokenCost != b.TokenCost {
			return a.TokenCost < b.TokenCost
		}
		return a.ID < b.ID
	})
}
