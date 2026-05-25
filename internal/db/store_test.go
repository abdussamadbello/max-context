package db

import (
	"path/filepath"
	"testing"
)

func TestSQLiteStoreImplementsStore(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	if err := Migrate(database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var s Store = NewSQLiteStore(database)

	// SymbolsInFile passes through.
	syms, err := s.SymbolsInFile("does-not-exist.go")
	if err != nil {
		t.Fatalf("SymbolsInFile: %v", err)
	}
	if len(syms) != 0 {
		t.Fatalf("want 0, got %d", len(syms))
	}
}
