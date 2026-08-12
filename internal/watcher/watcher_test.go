package watcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fastOpts keeps the debounce short so tests stay quick but still exercise the
// real timer path rather than a stub.
func fastOpts() *Options { return &Options{DebounceMs: 30} }

// waitFor returns the next path the watcher emits, or fails after timeout.
func waitFor(t *testing.T, ch <-chan string, timeout time.Duration) string {
	t.Helper()
	select {
	case p := <-ch:
		return p
	case <-time.After(timeout):
		t.Fatalf("no watcher event within %s", timeout)
		return ""
	}
}

// expectNothing fails if any event arrives within the window.
func expectNothing(t *testing.T, ch <-chan string, window time.Duration) {
	t.Helper()
	select {
	case p := <-ch:
		t.Fatalf("unexpected event for %q", p)
	case <-time.After(window):
	}
}

func startWatcher(t *testing.T, root string, ch chan string, opts *Options) {
	t.Helper()
	w, err := New(root, ch, opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// fsnotify registration is not instant on all platforms.
	time.Sleep(50 * time.Millisecond)
}

func TestWatcherReportsChangedSourceFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ch := make(chan string, 10)
	startWatcher(t, root, ch, fastOpts())

	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\nfunc F(){}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := waitFor(t, ch, 3*time.Second); got != "a.go" {
		t.Errorf("got %q, want a.go", got)
	}
}

// The freshness claim rests on this: an edit must reach the worker well inside
// the advertised window.
func TestWatcherDeliversWithinTwoSeconds(t *testing.T) {
	root := t.TempDir()
	ch := make(chan string, 10)
	startWatcher(t, root, ch, nil) // default 500ms debounce

	start := time.Now()
	if err := os.WriteFile(filepath.Join(root, "fresh.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := waitFor(t, ch, 2*time.Second)
	if got != "fresh.go" {
		t.Errorf("got %q, want fresh.go", got)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("took %s, longer than the documented 2s freshness window", elapsed)
	}
}

// Rapid successive writes must collapse into one reindex request.
func TestWatcherDebouncesRapidWrites(t *testing.T) {
	root := t.TempDir()
	ch := make(chan string, 20)
	startWatcher(t, root, ch, &Options{DebounceMs: 120})

	path := filepath.Join(root, "busy.go")
	for i := 0; i < 6; i++ {
		if err := os.WriteFile(path, []byte("package main\n// "+string(rune('a'+i))+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := waitFor(t, ch, 3*time.Second); got != "busy.go" {
		t.Errorf("got %q, want busy.go", got)
	}
	// Everything within the window coalesced; nothing more should follow.
	expectNothing(t, ch, 300*time.Millisecond)
}

func TestWatcherIgnoresUnsupportedExtensions(t *testing.T) {
	root := t.TempDir()
	ch := make(chan string, 10)
	startWatcher(t, root, ch, fastOpts())

	if err := os.WriteFile(filepath.Join(root, "image.png"), []byte("not code"), 0644); err != nil {
		t.Fatal(err)
	}
	expectNothing(t, ch, 400*time.Millisecond)
}

// Documents index as plain text regardless of any language restriction, so a
// markdown edit must still be reported.
func TestWatcherReportsDocumentsEvenWhenLanguagesRestricted(t *testing.T) {
	root := t.TempDir()
	ch := make(chan string, 10)
	startWatcher(t, root, ch, &Options{DebounceMs: 30, Extensions: []string{".go"}})

	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# hi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := waitFor(t, ch, 3*time.Second); got != "README.md" {
		t.Errorf("got %q, want README.md", got)
	}
}

func TestWatcherRestrictsToConfiguredExtensions(t *testing.T) {
	root := t.TempDir()
	ch := make(chan string, 10)
	startWatcher(t, root, ch, &Options{DebounceMs: 30, Extensions: []string{".go"}})

	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("x = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	expectNothing(t, ch, 400*time.Millisecond)
}

func TestWatcherSkipsIgnoredDirectories(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"node_modules", "vendor", ".git"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	ch := make(chan string, 10)
	startWatcher(t, root, ch, fastOpts())

	for _, dir := range []string{"node_modules", "vendor", ".git"} {
		if err := os.WriteFile(filepath.Join(root, dir, "x.go"), []byte("package x\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expectNothing(t, ch, 500*time.Millisecond)
}

// A directory created after startup must be watched too, or files added to a
// new package are never indexed.
func TestWatcherPicksUpNewDirectories(t *testing.T) {
	root := t.TempDir()
	ch := make(chan string, 10)
	startWatcher(t, root, ch, fastOpts())

	sub := filepath.Join(root, "pkg", "inner")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // let the new dirs register

	if err := os.WriteFile(filepath.Join(sub, "deep.go"), []byte("package inner\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := waitFor(t, ch, 3*time.Second)
	if got != "pkg/inner/deep.go" {
		t.Errorf("got %q, want pkg/inner/deep.go", got)
	}
}

// Writing .max-context/.reindex-queue asks for a full reindex, signalled by the
// empty-string sentinel, and the queue file is consumed.
func TestReindexQueueTriggersFullReindex(t *testing.T) {
	root := t.TempDir()
	mcDir := filepath.Join(root, ".max-context")
	if err := os.MkdirAll(mcDir, 0755); err != nil {
		t.Fatal(err)
	}
	ch := make(chan string, 10)
	startWatcher(t, root, ch, fastOpts())

	queue := filepath.Join(mcDir, ".reindex-queue")
	if err := os.WriteFile(queue, []byte("go\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := waitFor(t, ch, 3*time.Second); got != "" {
		t.Errorf("got %q, want the full-reindex sentinel \"\"", got)
	}
	// The watcher consumes the file so the request fires once.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(queue); os.IsNotExist(err) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error(".reindex-queue was not consumed")
}

// The regression this replaced: a full channel used to make the watcher discard
// paths, leaving files stale in an index that still reported itself healthy.
//
// The channel is deliberately left undrained until every debounce timer has
// fired, so the sends genuinely meet a full buffer — draining concurrently
// keeps the buffer clear and the old dropping code passes.
func TestWatcherDoesNotDropEventsWhenChannelIsFull(t *testing.T) {
	root := t.TempDir()
	const n = 12
	ch := make(chan string, 2) // smaller than the number of files changed
	startWatcher(t, root, ch, fastOpts())

	want := map[string]bool{}
	for i := 0; i < n; i++ {
		name := "f" + string(rune('a'+i)) + ".go"
		want[name] = true
		if err := os.WriteFile(filepath.Join(root, name), []byte("package main\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Let every debounce timer fire against the full buffer before reading.
	time.Sleep(500 * time.Millisecond)

	seen := map[string]bool{}
	deadline := time.After(10 * time.Second)
	for len(seen) < n {
		select {
		case p := <-ch:
			seen[p] = true
		case <-deadline:
			var missing []string
			for name := range want {
				if !seen[name] {
					missing = append(missing, name)
				}
			}
			t.Fatalf("only %d/%d changed files reached the worker; dropped: %v",
				len(seen), n, missing)
		}
	}
}

// Cancelling the context must stop the watcher and release anything waiting to
// send, rather than leaking a blocked goroutine.
func TestWatcherStopsOnContextCancel(t *testing.T) {
	root := t.TempDir()
	ch := make(chan string) // unbuffered and never read: a send would block
	w, err := New(root, ch, fastOpts())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(root, "x.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // let the debounce timer fire and block

	cancel()

	// send must return once done is closed.
	released := make(chan struct{})
	go func() { w.send("late.go"); close(released) }()
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Error("send did not unblock after the watcher stopped")
	}
}

func TestIsDockerfileBase(t *testing.T) {
	for _, tc := range []struct {
		base string
		want bool
	}{
		{"Dockerfile", true},
		{"Dockerfile.dev", true},
		{"Dockerfile.prod", true},
		{"dockerfile", false},
		{"MyDockerfile", false},
		{"main.go", false},
	} {
		if got := isDockerfileBase(tc.base); got != tc.want {
			t.Errorf("isDockerfileBase(%q) = %v, want %v", tc.base, got, tc.want)
		}
	}
}

func TestNewAppliesDebounceOption(t *testing.T) {
	ch := make(chan string, 1)
	w, err := New(t.TempDir(), ch, &Options{DebounceMs: 250})
	if err != nil {
		t.Fatal(err)
	}
	defer w.stop()
	if w.delay != 250*time.Millisecond {
		t.Errorf("delay = %s, want 250ms", w.delay)
	}
}

func TestNewDefaultsDebounceWhenUnset(t *testing.T) {
	ch := make(chan string, 1)
	for _, opts := range []*Options{nil, {}, {DebounceMs: 0}} {
		w, err := New(t.TempDir(), ch, opts)
		if err != nil {
			t.Fatal(err)
		}
		if w.delay != debounceMs*time.Millisecond {
			t.Errorf("opts %+v: delay = %s, want %dms", opts, w.delay, debounceMs)
		}
		w.stop()
	}
}
