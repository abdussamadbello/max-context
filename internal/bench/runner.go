package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Question describes one benchmark probe. Curated per-repo in benchmark/questions/.
type Question struct {
	ID               string   `json:"id"`
	Text             string   `json:"text"`
	Category         string   `json:"category"` // "lookup" | "trace" | "impact"
	Tool             string   `json:"mc_tool"`
	Terms            []string `json:"baseline_terms"`
	MCResponseTokens int      `json:"mc_response_tokens"`
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
}

// Run executes both baseline paths for each question and writes results.json + benchmark.md to OutDir.
func Run(root string, questions []Question, opts RunOptions) (*Results, error) {
	if err := os.MkdirAll(opts.OutDir, 0755); err != nil {
		return nil, err
	}

	res := &Results{Repo: opts.Repo}
	var sumMC, sumNaive, sumSkilled float64
	for _, q := range questions {
		naive, err := NaiveBaseline(root, q.Terms)
		if err != nil {
			return nil, fmt.Errorf("naive baseline %s: %w", q.ID, err)
		}
		skilled, err := SkilledBaseline(root, q.Terms)
		if err != nil {
			return nil, fmt.Errorf("skilled baseline %s: %w", q.ID, err)
		}
		mcTokens := q.MCResponseTokens
		if mcTokens == 0 {
			mcTokens = 1
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
