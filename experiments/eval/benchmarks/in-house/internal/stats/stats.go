// Package stats provides the small set of paired, non-parametric statistics the
// experiment reports at n≈40: Wilcoxon signed-rank, McNemar, and a bootstrap CI
// on the median paired delta. Deterministic (seeded) so reports reproduce.
package stats

import (
	"math"
	"sort"
)

// WilcoxonSignedRank returns the signed-rank statistic W and the number of
// non-zero pairs, for paired deltas (a_i - b_i). For small n, report W and n;
// callers map to significance via a table or normal approximation below.
func WilcoxonSignedRank(deltas []float64) (W float64, n int) {
	type ranked struct {
		abs  float64
		sign float64
	}
	var nz []ranked
	for _, d := range deltas {
		if d == 0 {
			continue
		}
		s := 1.0
		if d < 0 {
			s = -1.0
		}
		nz = append(nz, ranked{abs: math.Abs(d), sign: s})
	}
	n = len(nz)
	if n == 0 {
		return 0, 0
	}
	sort.Slice(nz, func(i, j int) bool { return nz[i].abs < nz[j].abs })
	// Average ranks for ties.
	ranks := make([]float64, n)
	i := 0
	for i < n {
		j := i
		for j < n && nz[j].abs == nz[i].abs {
			j++
		}
		avg := float64(i+1+j) / 2.0 // average of ranks (i+1)..j
		for k := i; k < j; k++ {
			ranks[k] = avg
		}
		i = j
	}
	var wPlus float64
	for k := range nz {
		if nz[k].sign > 0 {
			wPlus += ranks[k]
		}
	}
	return wPlus, n
}

// WilcoxonZ returns the normal-approximation z and two-sided p for W+ at n pairs.
// Valid only as an approximation (n>~10); for tiny n report descriptively.
func WilcoxonZ(wPlus float64, n int) (z, p float64) {
	if n == 0 {
		return 0, 1
	}
	nn := float64(n)
	mean := nn * (nn + 1) / 4
	sd := math.Sqrt(nn * (nn + 1) * (2*nn + 1) / 24)
	if sd == 0 {
		return 0, 1
	}
	z = (wPlus - mean) / sd
	p = 2 * (1 - normalCDF(math.Abs(z)))
	return z, p
}

// McNemar compares paired binary outcomes. b = #(A correct, B wrong),
// c = #(A wrong, B correct). Returns the (continuity-corrected) chi-square and
// two-sided p. The sign of (c-b) shows which arm wins more discordant pairs.
func McNemar(b, c int) (chi2, p float64) {
	if b+c == 0 {
		return 0, 1
	}
	diff := math.Abs(float64(b-c)) - 1
	if diff < 0 {
		diff = 0
	}
	chi2 = diff * diff / float64(b+c)
	p = 1 - chiSquare1CDF(chi2)
	return chi2, p
}

// MedianDelta returns the median of paired deltas.
func MedianDelta(deltas []float64) float64 {
	if len(deltas) == 0 {
		return 0
	}
	cp := append([]float64(nil), deltas...)
	sort.Float64s(cp)
	m := len(cp) / 2
	if len(cp)%2 == 1 {
		return cp[m]
	}
	return (cp[m-1] + cp[m]) / 2
}

// BootstrapMedianCI returns a percentile bootstrap CI for the median paired
// delta. seed makes it reproducible (no global rand). iters≈10000.
func BootstrapMedianCI(deltas []float64, iters int, seed uint64, alpha float64) (lo, hi float64) {
	n := len(deltas)
	if n == 0 {
		return 0, 0
	}
	rng := newPCG(seed)
	meds := make([]float64, iters)
	sample := make([]float64, n)
	for it := 0; it < iters; it++ {
		for i := 0; i < n; i++ {
			sample[i] = deltas[rng.intn(n)]
		}
		meds[it] = MedianDelta(sample)
	}
	sort.Float64s(meds)
	loIdx := int(alpha / 2 * float64(iters))
	hiIdx := int((1 - alpha/2) * float64(iters))
	if hiIdx >= iters {
		hiIdx = iters - 1
	}
	return meds[loIdx], meds[hiIdx]
}

// CliffsDelta is a non-parametric effect size in [-1,1]: P(a>b) - P(a<b) over
// all pairs. Here pass paired deltas; it measures how often deltas are >0 vs <0.
func CliffsDelta(deltas []float64) float64 {
	if len(deltas) == 0 {
		return 0
	}
	var gt, lt int
	for _, d := range deltas {
		if d > 0 {
			gt++
		} else if d < 0 {
			lt++
		}
	}
	return float64(gt-lt) / float64(len(deltas))
}

// --- numerics ---

func normalCDF(x float64) float64 { return 0.5 * (1 + math.Erf(x/math.Sqrt2)) }

// chiSquare1CDF is the CDF of chi-square with 1 df = erf(sqrt(x/2)).
func chiSquare1CDF(x float64) float64 {
	if x <= 0 {
		return 0
	}
	return math.Erf(math.Sqrt(x / 2))
}

// pcg is a tiny deterministic RNG (no global state, reproducible reports).
type pcg struct{ state, inc uint64 }

func newPCG(seed uint64) *pcg {
	p := &pcg{inc: (seed << 1) | 1}
	p.next()
	p.state += seed
	p.next()
	return p
}

func (p *pcg) next() uint32 {
	old := p.state
	p.state = old*6364136223846793005 + p.inc
	xorshifted := uint32(((old >> 18) ^ old) >> 27)
	rot := uint32(old >> 59)
	return (xorshifted >> rot) | (xorshifted << ((-rot) & 31))
}

func (p *pcg) intn(n int) int {
	return int(p.next() % uint32(n))
}
