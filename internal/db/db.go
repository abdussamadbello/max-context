package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const DriverName = "sqlite"

func Open(path string) (*sql.DB, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	db, err := sql.Open(DriverName, sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := applyPragmas(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return db, nil
}

func applyPragmas(db *sql.DB) error {
	// journal_mode is persistent database state, so set it once after opening.
	// Connection-local pragmas live in sqliteDSN and are applied by the driver to
	// every pooled connection. In particular, busy_timeout is already active
	// here, so concurrent one-shot CLI processes wait instead of racing this WAL
	// transition and failing immediately with SQLITE_BUSY.
	for _, p := range []string{
		"PRAGMA journal_mode = WAL",
	} {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("exec %s: %w", p, err)
		}
	}
	return nil
}

func sqliteDSN(path string) string {
	pragmas := []string{
		"busy_timeout(5000)",
		"foreign_keys(ON)",
		"synchronous(NORMAL)",
		"cache_size(-8000)",
		"mmap_size(268435456)",
		"temp_store(MEMORY)",
	}
	values := url.Values{}
	for _, pragma := range pragmas {
		values.Add("_pragma", pragma)
	}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + values.Encode()
}
