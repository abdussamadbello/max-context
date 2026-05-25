package db

import "database/sql"

// Store is the storage abstraction consumed by tools that need read access to the
// index. The current implementation is SQLite-backed; the interface exists so a
// future engine (DuckDB, etc.) can be added without rewriting tool handlers.
// New methods are added here only when a consumer actually needs them.
type Store interface {
	SymbolsInFile(filePath string) ([]Symbol, error)
	DB() *sql.DB // escape hatch for callers that still need raw SQL (e.g. recursive CTEs)
}

// SQLiteStore is the only Store implementation today.
type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

func (s *SQLiteStore) SymbolsInFile(filePath string) ([]Symbol, error) {
	return SymbolsInFile(s.db, filePath)
}

func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}
