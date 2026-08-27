package indexer

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
)

func docsTestDB(t *testing.T) (*sql.DB, *db.Queries) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "docs.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		t.Fatalf("PrepareQueries: %v", err)
	}
	t.Cleanup(func() { q.Close() })
	return database, q
}

func docRow(t *testing.T, database *sql.DB, path string) (kind, title string, found bool) {
	t.Helper()
	err := database.QueryRow(
		`SELECT kind, COALESCE(title,'') FROM documents WHERE file_path = ?`, path).Scan(&kind, &title)
	if err == sql.ErrNoRows {
		return "", "", false
	}
	if err != nil {
		t.Fatalf("docRow %s: %v", path, err)
	}
	return kind, title, true
}

// TestFullIndexPicksUpDocuments: a full Index() stores non-code files as
// documents (with markdown titles), skips lock files, and leaves code files
// in the functions table only.
func TestFullIndexPicksUpDocuments(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root, "main.go", "package app\n\nfunc Run() {}\n")
	writeFile(t, root, "docs/guide.md", "# User Guide\n\nHow to configure the indexer.\n")
	writeFile(t, root, "Dockerfile", "FROM golang:1.22\nRUN make build\n")
	writeFile(t, root, "config/app.yaml", "retries: 3\n")
	writeFile(t, root, "package-lock.json", `{"lockfileVersion": 3}`)

	database, q := docsTestDB(t)
	if err := Index(ctx, root, database, q); err != nil {
		t.Fatalf("Index: %v", err)
	}

	if kind, title, ok := docRow(t, database, "docs/guide.md"); !ok || kind != "markdown" || title != "User Guide" {
		t.Errorf("guide.md: kind=%q title=%q found=%v", kind, title, ok)
	}
	if kind, _, ok := docRow(t, database, "Dockerfile"); !ok || kind != "dockerfile" {
		t.Errorf("Dockerfile: kind=%q found=%v", kind, ok)
	}
	if kind, _, ok := docRow(t, database, "config/app.yaml"); !ok || kind != "yaml" {
		t.Errorf("app.yaml: kind=%q found=%v", kind, ok)
	}
	if _, _, ok := docRow(t, database, "package-lock.json"); ok {
		t.Error("package-lock.json must be skipped")
	}
	if _, _, ok := docRow(t, database, "main.go"); ok {
		t.Error("code files must not be stored as documents")
	}

	// FTS works end-to-end for documents.
	var n int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM documents_fts WHERE documents_fts MATCH ?`, `"configure"`).Scan(&n); err != nil {
		t.Fatalf("fts: %v", err)
	}
	if n != 1 {
		t.Errorf("FTS matches = %d, want 1", n)
	}
}

// TestIndexDocFileLifecycle: incremental add, modify, delete of a document.
func TestIndexDocFileLifecycle(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	database, _ := docsTestDB(t)

	writeFile(t, root, "notes.md", "# Notes\n\nfirst draft\n")
	if err := IndexDocFile(ctx, root, "notes.md", database); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, title, ok := docRow(t, database, "notes.md"); !ok || title != "Notes" {
		t.Fatalf("after add: title=%q found=%v", title, ok)
	}

	writeFile(t, root, "notes.md", "# Revised Notes\n\nsecond draft\n")
	if err := IndexDocFile(ctx, root, "notes.md", database); err != nil {
		t.Fatalf("modify: %v", err)
	}
	if _, title, _ := docRow(t, database, "notes.md"); title != "Revised Notes" {
		t.Errorf("after modify: title=%q, want Revised Notes", title)
	}
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM documents WHERE file_path = ?`, "notes.md").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("rows after modify = %d, want 1 (no duplicates)", n)
	}

	if err := os.Remove(filepath.Join(root, "notes.md")); err != nil {
		t.Fatal(err)
	}
	if err := IndexDocFile(ctx, root, "notes.md", database); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, ok := docRow(t, database, "notes.md"); ok {
		t.Error("document row must be gone after file deletion")
	}
}

func TestIndexDocFileRejectsNonUTF8WithoutDroppingLastGoodRow(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	database, _ := openIndexDB(t, root)
	defer database.Close()
	path := filepath.Join(root, "notes.md")
	if err := os.WriteFile(path, []byte("# Good\nsearchable text"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := IndexDocFile(ctx, root, "notes.md", database); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte{0xff, 0xfe}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := IndexDocFile(ctx, root, "notes.md", database); err == nil {
		t.Fatal("non-UTF-8 document should fail indexing")
	}
	var content string
	if err := database.QueryRow(`SELECT content FROM documents WHERE file_path = ?`, "notes.md").Scan(&content); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "searchable text") {
		t.Fatalf("last good document row was not preserved: %q", content)
	}
}

// TestIndexDocFileLeavesResolverCacheWarm: a document change must not touch
// the worker's delta-maintained resolver — only IndexFile may do that.
func TestIndexDocFileLeavesResolverCacheWarm(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	database, q := docsTestDB(t)

	writeFile(t, root, "a.go", "package app\n\nfunc A() {}\n")
	if err := Index(ctx, root, database, q); err != nil {
		t.Fatalf("Index: %v", err)
	}

	// Warm the cache through one incremental code reindex.
	cache := NewResolverCache()
	if err := IndexFile(ctx, root, "a.go", database, q, cache); err != nil {
		t.Fatalf("IndexFile: %v", err)
	}
	warm := cache.r
	if warm == nil {
		t.Fatal("cache should be warm after IndexFile")
	}

	writeFile(t, root, "readme.md", "# Readme\n")
	if err := IndexDocFile(ctx, root, "readme.md", database); err != nil {
		t.Fatalf("IndexDocFile: %v", err)
	}
	if cache.r != warm {
		t.Error("IndexDocFile must not rebuild or invalidate the resolver cache")
	}
}

// TestFullIndexDeterministic: with the parallel parse pool, two consecutive
// full indexes of the same tree must produce identical row ordering — results
// are aggregated by scan position, not completion order.
func TestFullIndexDeterministic(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root, "a.go", "package app\n\nfunc A() { B() }\n")
	writeFile(t, root, "b.go", "package app\n\nfunc B() { C() }\n")
	writeFile(t, root, "c.go", "package app\n\nfunc C() {}\n")
	writeFile(t, root, "d/d.go", "package d\n\nfunc D() {}\n")

	database, q := docsTestDB(t)

	snapshot := func() []string {
		t.Helper()
		rows, err := database.Query(`SELECT name, file_path, start_line FROM functions ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var name, fp string
			var line int
			if err := rows.Scan(&name, &fp, &line); err != nil {
				t.Fatal(err)
			}
			out = append(out, name+"|"+fp)
		}
		return out
	}

	if err := Index(ctx, root, database, q); err != nil {
		t.Fatal(err)
	}
	first := snapshot()
	if err := Index(ctx, root, database, q); err != nil {
		t.Fatal(err)
	}
	second := snapshot()
	if len(first) == 0 || len(first) != len(second) {
		t.Fatalf("snapshots: %d vs %d rows", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("row %d differs: %q vs %q", i, first[i], second[i])
		}
	}
}
