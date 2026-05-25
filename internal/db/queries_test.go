package db

import (
	"path/filepath"
	"testing"
)

func TestSymbolsInFile(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	if err := Migrate(database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	q, err := PrepareQueries(database)
	if err != nil {
		t.Fatalf("PrepareQueries: %v", err)
	}
	defer q.Close()

	// Seed two functions in two files.
	if _, err := q.InsertFunction.Exec("Foo", "internal/a.go", 10, 20, "go", 1, "func Foo(){}", "", "Foo()"); err != nil {
		t.Fatalf("insert Foo: %v", err)
	}
	if _, err := q.InsertFunction.Exec("Bar", "internal/a.go", 30, 40, "go", 1, "func Bar(){}", "", "Bar()"); err != nil {
		t.Fatalf("insert Bar: %v", err)
	}
	if _, err := q.InsertFunction.Exec("Baz", "internal/b.go", 5, 8, "go", 0, "func baz(){}", "", "baz()"); err != nil {
		t.Fatalf("insert Baz: %v", err)
	}

	syms, err := SymbolsInFile(database, "internal/a.go")
	if err != nil {
		t.Fatalf("SymbolsInFile: %v", err)
	}
	if len(syms) != 2 {
		t.Fatalf("want 2 symbols, got %d", len(syms))
	}
	names := map[string]bool{syms[0].Name: true, syms[1].Name: true}
	if !names["Foo"] || !names["Bar"] {
		t.Fatalf("missing expected symbols: %+v", syms)
	}

	empty, err := SymbolsInFile(database, "internal/missing.go")
	if err != nil {
		t.Fatalf("SymbolsInFile(missing): %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("want 0 for missing file, got %d", len(empty))
	}
}
