package stats

import (
	"math"
	"testing"
)

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestWilcoxon_AllPositive(t *testing.T) {
	// All deltas positive -> W+ equals sum of all ranks = n(n+1)/2.
	d := []float64{1, 2, 3, 4, 5}
	w, n := WilcoxonSignedRank(d)
	if n != 5 {
		t.Fatalf("n=%d", n)
	}
	if w != 15 { // 1+2+3+4+5
		t.Errorf("W+=%v, want 15", w)
	}
	z, p := WilcoxonZ(w, n)
	if z <= 0 || p >= 1 {
		t.Errorf("expected positive z and p<1, got z=%v p=%v", z, p)
	}
}

func TestWilcoxon_Symmetric(t *testing.T) {
	// Symmetric around zero -> W+ ≈ half total, z≈0.
	d := []float64{-2, -1, 1, 2}
	w, n := WilcoxonSignedRank(d)
	z, _ := WilcoxonZ(w, n)
	if !approx(z, 0, 1e-9) {
		t.Errorf("symmetric should give z≈0, got %v (W+=%v)", z, w)
	}
}

func TestWilcoxon_IgnoresZeros(t *testing.T) {
	_, n := WilcoxonSignedRank([]float64{0, 0, 3})
	if n != 1 {
		t.Errorf("zeros must be dropped, n=%d want 1", n)
	}
}

func TestMcNemar(t *testing.T) {
	// b=10 (A win), c=2 (B win): strong asymmetry -> notable chi2.
	chi2, p := McNemar(10, 2)
	if chi2 <= 0 || p >= 1 {
		t.Errorf("expected chi2>0 p<1, got chi2=%v p=%v", chi2, p)
	}
	// Equal discordant -> chi2≈0, p≈1.
	chi2e, pe := McNemar(5, 5)
	if chi2e != 0 || !approx(pe, 1, 1e-9) {
		t.Errorf("equal discordant should give chi2=0 p=1, got %v %v", chi2e, pe)
	}
}

func TestMedianAndCliffs(t *testing.T) {
	d := []float64{-1, 2, 2, 3}
	if m := MedianDelta(d); m != 2 {
		t.Errorf("median=%v want 2", m)
	}
	// 3 positive, 1 negative -> (3-1)/4 = 0.5
	if cd := CliffsDelta(d); !approx(cd, 0.5, 1e-9) {
		t.Errorf("cliffs=%v want 0.5", cd)
	}
}

func TestBootstrapDeterministic(t *testing.T) {
	d := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	lo1, hi1 := BootstrapMedianCI(d, 2000, 42, 0.05)
	lo2, hi2 := BootstrapMedianCI(d, 2000, 42, 0.05)
	if lo1 != lo2 || hi1 != hi2 {
		t.Errorf("bootstrap not deterministic: (%v,%v) vs (%v,%v)", lo1, hi1, lo2, hi2)
	}
	if lo1 > hi1 {
		t.Errorf("lo>hi: %v %v", lo1, hi1)
	}
}
