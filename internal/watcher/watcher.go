package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/maxcontext/max-context/pkg/treesitter"
)

const debounceMs = 500

type Watcher struct {
	root     string
	ignore   map[string]bool
	exts     map[string]bool
	w        *fsnotify.Watcher
	ch       chan<- string
	mu       sync.Mutex
	pending  map[string]*time.Timer
	done     chan struct{}
}

func New(root string, reindexCh chan<- string) (*Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	ignore := map[string]bool{
		"node_modules": true, ".git": true, "dist": true, "build": true,
		"vendor": true, "__pycache__": true, ".max-context": true,
	}
	exts := make(map[string]bool)
	for _, ext := range treesitter.SupportedExtensions() {
		exts[ext] = true
	}
	return &Watcher{
		root:    root,
		ignore:  ignore,
		exts:    exts,
		w:       w,
		ch:      reindexCh,
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
	for {
		select {
		case <-ctx.Done():
			w.w.Close()
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
			select {
			case w.ch <- "":
			default:
			}
		}
		return
	}
	ext := filepath.Ext(ev.Name)
	if !w.exts[ext] {
		return
	}
	w.debounce(rel)
}

func (w *Watcher) debounce(relPath string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if t, ok := w.pending[relPath]; ok {
		t.Reset(debounceMs * time.Millisecond)
		return
	}
	w.pending[relPath] = time.AfterFunc(debounceMs*time.Millisecond, func() {
		w.mu.Lock()
		delete(w.pending, relPath)
		w.mu.Unlock()
		select {
		case w.ch <- relPath:
		default:
		}
	})
}
