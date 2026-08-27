package indexer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
)

func TestParseSourceFilesReturnsReadFailures(t *testing.T) {
	_, err := parseSourceFiles(context.Background(), t.TempDir(), []string{"missing.go"})
	if err == nil {
		t.Fatal("missing source file should fail a full-index parse batch")
	}
	if !strings.Contains(err.Error(), "missing.go: read:") {
		t.Fatalf("error does not identify failed file: %v", err)
	}
}

func TestIndexTracksSourceFilesWithoutSymbols(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(root, ".max-context", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	if err := Index(context.Background(), root, database, q); err != nil {
		t.Fatal(err)
	}
	var language string
	var functions, types, imports int
	if err := database.QueryRow(`
		SELECT language, function_count, type_count, import_count
		FROM file_summaries WHERE file_path = 'empty.go'`).Scan(&language, &functions, &types, &imports); err != nil {
		t.Fatal(err)
	}
	if language != "go" || functions != 0 || types != 0 || imports != 0 {
		t.Fatalf("empty.go summary = language %q, counts %d/%d/%d", language, functions, types, imports)
	}
}
