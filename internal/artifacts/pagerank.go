package artifacts

import (
	"database/sql"
	"sort"
)

// RankedFunc is one function with its PageRank score over the call graph.
type RankedFunc struct {
	ID       int64
	Name     string
	FilePath string
	Score    float64
}

const (
	pagerankDamping    = 0.85
	pagerankIterations = 30
	pagerankEpsilon    = 1e-6
)

// PageRankFunctions ranks every indexed function by PageRank over resolved
// call edges, highest first (deterministic tiebreak on name then file).
// A function many distinct paths lead to ranks high — a structural "key
// function" signal that beats raw caller counts (the Aider repo-map insight).
// Interface-dispatch edges are excluded: they are low-confidence fan-out
// guesses and would let interface methods dominate the ranking.
func PageRankFunctions(database *sql.DB) ([]RankedFunc, error) {
	rows, err := database.Query(`SELECT id, name, file_path FROM functions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var funcs []RankedFunc
	idx := make(map[int64]int)
	for rows.Next() {
		var f RankedFunc
		if err := rows.Scan(&f.ID, &f.Name, &f.FilePath); err != nil {
			rows.Close()
			return nil, err
		}
		idx[f.ID] = len(funcs)
		funcs = append(funcs, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	n := len(funcs)
	if n == 0 {
		return nil, nil
	}

	edgeRows, err := database.Query(`
		SELECT caller_id, callee_id FROM calls
		WHERE callee_id IS NOT NULL AND resolution != 'interface-dispatch'`)
	if err != nil {
		return nil, err
	}
	outlinks := make([][]int, n)
	for edgeRows.Next() {
		var caller, callee int64
		if err := edgeRows.Scan(&caller, &callee); err != nil {
			edgeRows.Close()
			return nil, err
		}
		ci, ok1 := idx[caller]
		ti, ok2 := idx[callee]
		if ok1 && ok2 && ci != ti {
			outlinks[ci] = append(outlinks[ci], ti)
		}
	}
	edgeRows.Close()
	if err := edgeRows.Err(); err != nil {
		return nil, err
	}

	// Power iteration with dangling-mass redistribution.
	rank := make([]float64, n)
	next := make([]float64, n)
	for i := range rank {
		rank[i] = 1.0 / float64(n)
	}
	for iter := 0; iter < pagerankIterations; iter++ {
		dangling := 0.0
		for i := range next {
			next[i] = 0
		}
		for i, outs := range outlinks {
			if len(outs) == 0 {
				dangling += rank[i]
				continue
			}
			share := rank[i] / float64(len(outs))
			for _, t := range outs {
				next[t] += share
			}
		}
		base := (1-pagerankDamping)/float64(n) + pagerankDamping*dangling/float64(n)
		delta := 0.0
		for i := range next {
			v := base + pagerankDamping*next[i]
			d := v - rank[i]
			if d < 0 {
				d = -d
			}
			delta += d
			rank[i] = v
		}
		if delta < pagerankEpsilon {
			break
		}
	}

	for i := range funcs {
		funcs[i].Score = rank[i]
	}
	sort.Slice(funcs, func(a, b int) bool {
		if funcs[a].Score != funcs[b].Score {
			return funcs[a].Score > funcs[b].Score
		}
		if funcs[a].Name != funcs[b].Name {
			return funcs[a].Name < funcs[b].Name
		}
		return funcs[a].FilePath < funcs[b].FilePath
	})
	return funcs, nil
}

// FileScores aggregates function PageRank per file.
func FileScores(ranked []RankedFunc) map[string]float64 {
	out := make(map[string]float64)
	for _, f := range ranked {
		out[f.FilePath] += f.Score
	}
	return out
}
