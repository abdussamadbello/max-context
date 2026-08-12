package setup

import "testing"

// applyHarness runs one harness by name, failing the test if it is not
// registered — so a typo in a table-driven test is a clear failure rather than
// a silently skipped case.
func applyHarness(t *testing.T, target, root string, r *Report) error {
	t.Helper()
	h, ok := lookupHarness(target)
	if !ok {
		t.Fatalf("harness %q is not registered; known: %v", target, HarnessNames())
	}
	return h.apply(root, r)
}

// fakeHome points home-relative harnesses at a temp directory for the duration
// of one test, so the suite never writes to the developer's real ~/.hermes.
func fakeHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := userHomeDir
	userHomeDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { userHomeDir = prev })
	return dir
}
