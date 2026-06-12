package indexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeScanFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scanPaths(t *testing.T, sc *Scanner) map[string]bool {
	t.Helper()
	files, err := sc.Scan()
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]bool, len(files))
	for _, f := range files {
		out[filepath.ToSlash(f)] = true
	}
	return out
}

func TestScanIncludeGlobs(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "src/a.go", "package a\n")
	writeScanFile(t, root, "other/b.go", "package b\n")
	writeScanFile(t, root, "src/c.py", "x = 1\n")

	sc := &Scanner{Root: root, Includes: []string{"src/*.go"}}
	got := scanPaths(t, sc)
	if !got["src/a.go"] || got["other/b.go"] || got["src/c.py"] {
		t.Errorf("include filter wrong, got %v", got)
	}

	// Basename-style include matches at any depth.
	sc = &Scanner{Root: root, Includes: []string{"*.py"}}
	got = scanPaths(t, sc)
	if !got["src/c.py"] || got["src/a.go"] {
		t.Errorf("basename include wrong, got %v", got)
	}
}

func TestScanExtraExcludePatterns(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "keep.go", "package main\n")
	writeScanFile(t, root, "generated/skip.go", "package gen\n")

	ignore, err := NewIgnoreMatcherWithExtra(root, []string{"generated/"})
	if err != nil {
		t.Fatal(err)
	}
	sc := &Scanner{Root: root, Ignore: ignore}
	got := scanPaths(t, sc)
	if !got["keep.go"] || got["generated/skip.go"] {
		t.Errorf("extra exclude wrong, got %v", got)
	}
}

func TestScanMaxFileSize(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "small.go", "package a\n")
	writeScanFile(t, root, "big.go", "package a\n// "+strings.Repeat("x", 4096)+"\n")

	sc := &Scanner{Root: root, MaxFileSize: 1024}
	got := scanPaths(t, sc)
	if !got["small.go"] || got["big.go"] {
		t.Errorf("size gate wrong, got %v", got)
	}
}

func TestScanLanguageExtensionRestriction(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "a.go", "package a\n")
	writeScanFile(t, root, "b.py", "x = 1\n")

	sc := &Scanner{Root: root, Extensions: []string{".go"}}
	got := scanPaths(t, sc)
	if !got["a.go"] || got["b.py"] {
		t.Errorf("extension restriction wrong, got %v", got)
	}
}

func TestOptionsSkipsFile(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "src/a.go", "package a\n")
	writeScanFile(t, root, "gen/b.go", "package b\n")

	o := &Options{Include: []string{"src/*.go"}, Exclude: []string{"gen/"}, MaxFileSize: 1024}
	excl := &IgnoreMatcher{patterns: o.Exclude}

	if o.skipsFile(root, "src/a.go", excl) {
		t.Error("src/a.go should not be skipped")
	}
	if !o.skipsFile(root, "gen/b.go", excl) {
		t.Error("gen/b.go should be skipped via exclude")
	}
	if !o.skipsFile(root, "elsewhere/c.go", excl) {
		t.Error("elsewhere/c.go should be skipped via include filter")
	}
	// Deleted file (Stat fails) must not be skipped by the size gate.
	if (&Options{MaxFileSize: 10}).skipsFile(root, "src/missing.go", nil) {
		t.Error("missing file should not be skipped (deletion must proceed)")
	}
	var nilOpts *Options
	if nilOpts.skipsFile(root, "anything.go", nil) {
		t.Error("nil options must never skip")
	}
}
