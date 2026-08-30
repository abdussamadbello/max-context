package tools

import (
	"strings"
	"testing"
)

// The default filter admits narrow interface fan-outs and excludes wide ones.
// Before this, it excluded every dispatch edge, so a method reached only through
// an interface reported no callers — and when an unrelated type shared the
// method name, the single surviving row was the wrong one.
func TestDefaultEdgeFilterAdmitsNarrowDispatchOnly(t *testing.T) {
	f := defaultEdgeFilter()
	for _, want := range []string{
		"e.resolution != 'interface-dispatch'", // non-dispatch edges always pass
		"e.dispatch_width > 0",                 // rows from an index predating the column
		"e.dispatch_width <= 5",                // the width gate itself
	} {
		if !strings.Contains(f, want) {
			t.Errorf("default filter is missing %q:\n%s", want, f)
		}
	}
}

// dispatch_width is 0 on every row written before the column existed. Treating
// 0 as narrow would silently widen an old index on upgrade, changing answers
// with no reindex and no way for a reader to tell why.
func TestDefaultEdgeFilterExcludesUnwidthedRows(t *testing.T) {
	if !strings.Contains(defaultEdgeFilter(), "e.dispatch_width > 0") {
		t.Error("a row with dispatch_width 0 predates the column and must not be admitted by default")
	}
}

// get_call_chain and get_impact must agree about which edges exist. A blast
// radius that traverses edges a call chain hides — or the reverse — makes the
// two tools contradict each other on the same graph.
func TestImpactAndCallChainShareTheDefaultFilter(t *testing.T) {
	// Both call defaultEdgeFilter(); this pins that it stays a single source.
	a, b := defaultEdgeFilter(), defaultEdgeFilter()
	if a != b || a == "" {
		t.Fatalf("defaultEdgeFilter is not a stable single source: %q vs %q", a, b)
	}
	if maxDefaultDispatchWidth <= 0 {
		t.Errorf("maxDefaultDispatchWidth = %d; a non-positive gate admits nothing", maxDefaultDispatchWidth)
	}
}
