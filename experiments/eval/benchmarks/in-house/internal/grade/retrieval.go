// Package grade scores agent answers. Retrieval/impact grading is OBJECTIVE
// (set precision/recall vs a hand-verified key, no LLM). Edit grading uses a
// blind, different-model LLM judge against a pre-registered rubric.
package grade

import (
	"regexp"
	"sort"
	"strings"

	"github.com/maxcontext/eval/internal/spec"
)

// SetScore is precision/recall/F1 of a predicted set vs a gold set.
type SetScore struct {
	Precision float64  `json:"precision"`
	Recall    float64  `json:"recall"`
	F1        float64  `json:"f1"`
	Hit       bool     `json:"hit"`       // at least one gold item found
	Predicted []string `json:"predicted"` // what we extracted from the answer
	Matched   []string `json:"matched"`   // gold items the answer covered
	Missed    []string `json:"missed"`    // gold items the answer omitted

	// LeadFile is the first file path the answer mentions — for definition
	// queries this is the file the answer ENDORSES (models lead with their
	// conclusion). LeadCorrect is true when it matches a gold file. This avoids
	// penalizing answers that name extra files only to DISMISS them
	// (FINDINGS: the set-F1 precision artifact on overloaded names).
	LeadFile    string `json:"lead_file,omitempty"`
	LeadCorrect bool   `json:"lead_correct"`
}

// filePathRe matches repo-relative-ish source paths in free text, e.g.
// httpx/_client.py, src/core/index.ts, packages/zod/src/types.ts:42.
var filePathRe = regexp.MustCompile(`[A-Za-z0-9_./-]+\.(?:py|ts|tsx|js|jsx|go)`)

// ExtractFiles pulls candidate repo-relative file paths from answer text,
// normalizing away backticks, line-number suffixes, and leading ./ . Results are
// sorted (set semantics).
func ExtractFiles(answer string) []string {
	out := extractFilesInOrder(answer)
	sort.Strings(out)
	return out
}

// extractFilesInOrder is like ExtractFiles but preserves first-mention order,
// so the caller can identify the LEAD (affirmed) file.
func extractFilesInOrder(answer string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range filePathRe.FindAllString(answer, -1) {
		p := strings.TrimPrefix(m, "./")
		p = strings.Trim(p, "`'\"")
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// GradeRetrieval scores a retrieval answer against the key. It reports BOTH:
//   - set P/R/F1 (transparency; can be deflated by dismissed extra files), and
//   - the lead-file check (the first/affirmed file), which is the honest signal
//     for single-answer "where is X defined?" queries.
func GradeRetrieval(answer string, key spec.Key) SetScore {
	ordered := extractFilesInOrder(answer)
	gold := key.ExpectedFiles
	s := scoreSets(append([]string(nil), ordered...), gold)
	if len(ordered) > 0 {
		s.LeadFile = ordered[0]
		for _, g := range gold {
			if pathsMatch(ordered[0], g) {
				s.LeadCorrect = true
				break
			}
		}
	}
	return s
}

// scoreSets computes P/R/F1 with suffix-tolerant matching. Precision is over
// predicted paths that match SOME gold (so listing extra unrelated files hurts
// precision); recall is over gold paths covered.
func scoreSets(predicted, gold []string) SetScore {
	s := SetScore{Predicted: predicted}
	if len(gold) == 0 {
		return s
	}
	matchedGold := map[string]bool{}
	truePos := 0
	for _, p := range predicted {
		hitAny := false
		for _, g := range gold {
			if pathsMatch(p, g) {
				hitAny = true
				matchedGold[g] = true
			}
		}
		if hitAny {
			truePos++
		}
	}
	for _, g := range gold {
		if matchedGold[g] {
			s.Matched = append(s.Matched, g)
		} else {
			s.Missed = append(s.Missed, g)
		}
	}
	if len(predicted) > 0 {
		s.Precision = float64(truePos) / float64(len(predicted))
	}
	s.Recall = float64(len(s.Matched)) / float64(len(gold))
	if s.Precision+s.Recall > 0 {
		s.F1 = 2 * s.Precision * s.Recall / (s.Precision + s.Recall)
	}
	s.Hit = len(s.Matched) > 0
	return s
}

// pathsMatch is true if a and b are equal or one is a path-suffix of the other.
func pathsMatch(a, b string) bool {
	if a == b {
		return true
	}
	return strings.HasSuffix(a, "/"+b) || strings.HasSuffix(b, "/"+a)
}

// symbolRe matches identifier tokens (function/method/type names) in answer text.
var symbolRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// GradeSymbols scores a relationship answer ("list all callers of X", "blast
// radius of Y") by whether the answer NAMES each gold symbol. This is the metric
// where an index should beat grep: recall of the true caller/dependent set,
// including indirect or doc-noise-obscured ones grep tends to miss.
//
// Recall = (gold symbols named) / (all gold symbols). Precision here is noisy
// (prose mentions many identifiers), so the headline is recall; we still report
// precision for transparency.
func GradeSymbols(answer string, key spec.Key) SetScore {
	gold := key.ExpectedSymbols
	// Build a set of identifiers present in the answer.
	present := map[string]bool{}
	for _, tok := range symbolRe.FindAllString(answer, -1) {
		present[strings.ToLower(tok)] = true
	}
	s := SetScore{}
	if len(gold) == 0 {
		return s
	}
	for _, g := range gold {
		if present[strings.ToLower(g)] {
			s.Matched = append(s.Matched, g)
		} else {
			s.Missed = append(s.Missed, g)
		}
	}
	s.Recall = float64(len(s.Matched)) / float64(len(gold))
	s.Hit = len(s.Matched) > 0
	// Precision proxy: of the gold-set size worth of named identifiers, how many
	// matched. Kept simple; recall is the meaningful relationship metric.
	s.Precision = s.Recall
	s.F1 = s.Recall
	return s
}
