package watcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/maxcontext/max-context/internal/indexer"
	"github.com/maxcontext/max-context/pkg/treesitter"
)

// isDockerfileBase reports whether a basename is a Dockerfile (which has no
// extension for the exts map to match).
func isDockerfileBase(base string) bool {
	return base == "Dockerfile" || strings.HasPrefix(base, "Dockerfile.")
}

const debounceMs = 500

// Options carries optional watcher configuration from .max-context/config.json.
type Options struct {
	DebounceMs int      // event debounce in milliseconds (0 = default 500)
	Extensions []string // restrict to these file extensions (nil = all supported)
}

type Watcher struct {
	root     string
	ignore   map[string]bool
	exts     map[string]bool
	w        *fsnotify.Watcher
	ch       chan<- string
	delay    time.Duration
	mu       sync.Mutex
	pending  map[string]*time.Timer
	done     chan struct{}
	stopOnce sync.Once
}

func New(root string, reindexCh chan<- string, opts ...*Options) (*Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	ignore := map[string]bool{
		"node_modules": true, ".git": true, "dist": true, "build": true,
		"vendor": true, "__pycache__": true, ".max-context": true,
	}
	delay := debounceMs * time.Millisecond
	extList := treesitter.SupportedExtensions()
	if len(opts) > 0 && opts[0] != nil {
		if opts[0].DebounceMs > 0 {
			delay = time.Duration(opts[0].DebounceMs) * time.Millisecond
		}
		if len(opts[0].Extensions) > 0 {
			extList = opts[0].Extensions
		}
	}
	exts := make(map[string]bool)
	for _, ext := range extList {
		exts[ext] = true
	}
	// Always watch document files (markdown/yaml/json/...): they index as plain
	// text regardless of any language restriction on code files.
	for ext := range indexer.DocExtensions {
		exts[ext] = true
	}
	return &Watcher{
		root:    root,
		ignore:  ignore,
		exts:    exts,
		w:       w,
		ch:      reindexCh,
		delay:   delay,
		pending: make(map[string]*time.Timer),
		done:    make(chan struct{}),
	}, nil
}

func (w *Watcher) Start(ctx context.Context) error {
	absRoot, _ := filepath.Abs(w.root)
	if err := w.addRecursive(absRoot); err != nil {
		return err
	}
	// Watch .max-context so we can react to .reindex-queue (Phase 4)
	reindexDir := filepath.Join(absRoot, ".max-context")
	if info, err := os.Stat(reindexDir); err == nil && info.IsDir() {
		_ = w.w.Add(reindexDir)
	}
	go w.run(ctx)
	return nil
}

func (w *Watcher) addRecursive(dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if w.ignore[base] {
				return filepath.SkipDir
			}
			_ = w.w.Add(path)
		}
		return nil
	})
}

func (w *Watcher) run(ctx context.Context) {
	defer w.stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.w.Events:
			if !ok {
				return
			}
			w.handle(ev)
		case _, ok := <-w.w.Errors:
			if !ok {
				return
			}
		}
	}
}

// stop closes the watcher and releases anything blocked in send. Safe to call
// more than once.
func (w *Watcher) stop() {
	w.stopOnce.Do(func() {
		close(w.done)
		_ = w.w.Close()
	})
}

// send hands a path to the indexing worker, waiting for room rather than
// dropping the event.
//
// This used to be a non-blocking send with a `default:` that discarded the path
// whenever the buffer was full — so a bulk change (a branch switch, a big
// rebuild) silently left those files stale in an index that reports itself
// healthy. Callers run on their own goroutine, never on the fsnotify event
// loop, so waiting here cannot stall event delivery; w.done unblocks it at
// shutdown so nothing leaks.
func (w *Watcher) send(path string) {
	select {
	case w.ch <- path:
	case <-w.done:
	}
}

func (w *Watcher) handle(ev fsnotify.Event) {
	rel, err := filepath.Rel(w.root, ev.Name)
	if err != nil {
		return
	}
	rel = filepath.ToSlash(rel)
	base := filepath.Base(ev.Name)
	if w.ignore[base] {
		return
	}
	if ev.Op&fsnotify.Create != 0 {
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
			_ = w.addRecursive(ev.Name)
			return
		}
	}
	if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
		return
	}
	// Phase 4: .max-context/.reindex-queue triggers full reindex (sentinel "")
	if base == ".reindex-queue" && filepath.Base(filepath.Dir(ev.Name)) == ".max-context" {
		if ev.Op&(fsnotify.Create|fsnotify.Write) != 0 {
			_ = os.Remove(ev.Name)
			// On its own goroutine: handle runs on the fsnotify event loop, and
			// blocking here would stop the watcher from seeing further events.
			go w.send("")
		}
		return
	}
	ext := filepath.Ext(ev.Name)
	if !w.exts[ext] && !isDockerfileBase(base) {
		return
	}
	w.debounce(rel)
}

func (w *Watcher) debounce(relPath string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if t, ok := w.pending[relPath]; ok {
		t.Reset(w.delay)
		return
	}
	w.pending[relPath] = time.AfterFunc(w.delay, func() {
		w.mu.Lock()
		delete(w.pending, relPath)
		w.mu.Unlock()
		w.send(relPath)
	})
}
