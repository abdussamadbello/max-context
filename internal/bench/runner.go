package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maxcontext/max-context/internal/indexer"
)

// Question describes one benchmark probe. Curated per-repo in benchmark/questions/.
//
// There is deliberately no recorded token count for the max-context side. An
// earlier version carried one, hand-written per question, while only the
// baselines were computed by running grep+read — so the published ratio moved
// when the repo grew and never when the tool output changed, and drifted until
// it understated get_call_chain by up to 67x. Both sides are measured now.
type Question struct {
	ID       string          `json:"id"`
	Text     string          `json:"text"`
	Category string          `json:"category"` // "lookup" | "trace" | "impact"
	Tool     string          `json:"mc_tool"`
	Terms    []string        `json:"baseline_terms"`
	MCArgs   json.RawMessage `json:"mc_args"` // arguments the tool is invoked with
}

// QuestionResult captures both paths for one question.
type QuestionResult struct {
	ID            string `json:"id"`
	Text          string `json:"text"`
	Category      string `json:"category"`
	MCTokens      int    `json:"mc_tokens"`
	NaiveTokens   int    `json:"naive_tokens"`
	SkilledTokens int    `json:"skilled_tokens"`
	NaiveRatio    int    `json:"naive_ratio"`
	SkilledRatio  int    `json:"skilled_ratio"`
}

// Results is the top-level output structure.
type Results struct {
	Repo      string           `json:"repo"`
	Questions []QuestionResult `json:"questions"`
	Summary   struct {
		QuestionCount   int     `json:"question_count"`
		AvgMC           float64 `json:"avg_mc_tokens"`
		AvgNaive        float64 `json:"avg_naive_tokens"`
		AvgSkilled      float64 `json:"avg_skilled_tokens"`
		NaiveSavingsX   float64 `json:"naive_savings_x"`
		SkilledSavingsX float64 `json:"skilled_savings_x"`
	} `json:"summary"`
}

type RunOptions struct {
	OutDir string
	Repo   string

	// InvokeTool runs one max-context tool and returns the exact text the model
	// would receive. Required: the max-context column is measured by calling
	// this, never asserted. Injected rather than imported so this package keeps
	// no dependency on the tool layer and can be tested with a stub.
	InvokeTool func(tool string, args json.RawMessage) (string, error)
}

// Run executes both baseline paths for each question and writes results.json + benchmark.md to OutDir.
func Run(root string, questions []Question, opts RunOptions) (*Results, error) {
	if err := os.MkdirAll(opts.OutDir, 0755); err != nil {
		return nil, err
	}

	// Both baselines walk the same file set max-context indexes. Without this
	// the comparison rewarded max-context for files it never indexed: grep paid
	// for every byte under experiments/, the index did not.
	filter, err := indexer.NewIgnoreMatcherWithExtra(root, nil)
	if err != nil {
		return nil, fmt.Errorf("build repo filter: %w", err)
	}

	if opts.InvokeTool == nil {
		return nil, fmt.Errorf("RunOptions.InvokeTool is required: the max-context side must be measured, not assumed")
	}
	counter, err := NewCounter()
	if err != nil {
		return nil, fmt.Errorf("token counter: %w", err)
	}

	res := &Results{Repo: opts.Repo}
	var sumMC, sumNaive, sumSkilled float64
	for _, q := range questions {
		naive, err := NaiveBaseline(root, q.Terms, filter)
		if err != nil {
			return nil, fmt.Errorf("naive baseline %s: %w", q.ID, err)
		}
		skilled, err := SkilledBaseline(root, q.Terms, filter)
		if err != nil {
			return nil, fmt.Errorf("skilled baseline %s: %w", q.ID, err)
		}
		// A term that matches nothing is not a hard baseline, it is a broken
		// question: the ratio collapses to zero and quietly drags the average
		// down. The mirror of the empty-tool-response check below — neither side
		// may score a question it did not actually answer.
		if skilled == 0 || naive == 0 {
			return nil, fmt.Errorf("%s: baseline_terms %v match nothing in %s; the question is stale or the term is wrong",
				q.ID, q.Terms, root)
		}
		// Measured the same way as the baselines: run the thing, count what
		// comes back.
		resp, err := opts.InvokeTool(q.Tool, q.MCArgs)
		if err != nil {
			return nil, fmt.Errorf("invoke %s for %s: %w", q.Tool, q.ID, err)
		}
		mcTokens, err := counter.Count(resp)
		if err != nil {
			return nil, fmt.Errorf("count max-context response tokens for %s: %w", q.ID, err)
		}
		if mcTokens == 0 {
			return nil, fmt.Errorf("%s: %s returned an empty response; the question or its mc_args is stale", q.ID, q.Tool)
		}

		qr := QuestionResult{
			ID:            q.ID,
			Text:          q.Text,
			Category:      q.Category,
			MCTokens:      mcTokens,
			NaiveTokens:   naive,
			SkilledTokens: skilled,
			NaiveRatio:    naive / mcTokens,
			SkilledRatio:  skilled / mcTokens,
		}
		res.Questions = append(res.Questions, qr)
		sumMC += float64(mcTokens)
		sumNaive += float64(naive)
		sumSkilled += float64(skilled)
	}
	n := float64(len(questions))
	if n > 0 {
		res.Summary.QuestionCount = len(questions)
		res.Summary.AvgMC = sumMC / n
		res.Summary.AvgNaive = sumNaive / n
		res.Summary.AvgSkilled = sumSkilled / n
		if res.Summary.AvgMC > 0 {
			res.Summary.NaiveSavingsX = res.Summary.AvgNaive / res.Summary.AvgMC
			res.Summary.SkilledSavingsX = res.Summary.AvgSkilled / res.Summary.AvgMC
		}
	}

	sort.Slice(res.Questions, func(i, j int) bool { return res.Questions[i].ID < res.Questions[j].ID })

	body, _ := json.MarshalIndent(res, "", "  ")
	if err := os.WriteFile(filepath.Join(opts.OutDir, "results.json"), body, 0644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(opts.OutDir, "benchmark.md"), []byte(renderMarkdown(res)), 0644); err != nil {
		return nil, err
	}
	return res, nil
}

func renderMarkdown(r *Results) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Benchmark — %s\n\n", r.Repo))
	b.WriteString(fmt.Sprintf("**Question count:** %d\n\n", r.Summary.QuestionCount))
	b.WriteString("| Path | Avg tokens | Savings vs max-context |\n|---|---|---|\n")
	b.WriteString(fmt.Sprintf("| max-context | %.0f | 1× |\n", r.Summary.AvgMC))
	b.WriteString(fmt.Sprintf("| Naive Grep+Read | %.0f | %.1f× |\n", r.Summary.AvgNaive, r.Summary.NaiveSavingsX))
	b.WriteString(fmt.Sprintf("| Skilled Grep+Read | %.0f | %.1f× |\n\n", r.Summary.AvgSkilled, r.Summary.SkilledSavingsX))
	b.WriteString("## Per-question\n\n")
	b.WriteString("| ID | Category | Text | MC | Naive | Skilled |\n|---|---|---|---|---|---|\n")
	for _, q := range r.Questions {
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %d | %d | %d |\n", q.ID, q.Category, q.Text, q.MCTokens, q.NaiveTokens, q.SkilledTokens))
	}
	return b.String()
}
