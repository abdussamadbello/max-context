package artifacts

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Status struct {
	Healthy              bool      `json:"healthy"`
	LastFullIndex        time.Time `json:"lastFullIndex"`
	LastIncrementalIndex time.Time `json:"lastIncrementalIndex"`
	TotalFunctions       int       `json:"totalFunctions"`
	TotalFiles           int       `json:"totalFiles"`
	DirtyFiles           []string  `json:"dirtyFiles"`
	ReindexInProgress    bool      `json:"reindexInProgress"`
	Version              string    `json:"version"`
}

func WriteStatus(dir string, s *Status) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	p := filepath.Join(dir, "status.json")
	tmp := p + ".tmp"
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func ReadStatus(dir string) (*Status, error) {
	data, err := os.ReadFile(filepath.Join(dir, "status.json"))
	if err != nil {
		return nil, err
	}
	var s Status
	err = json.Unmarshal(data, &s)
	return &s, err
}

// WriteIndexStatus refreshes status.json from the live database health. It is
// safe to call after both successful and failed indexing attempts.
func WriteIndexStatus(dir string, database *sql.DB, version string) error {
	var totalFuncs, totalFiles int
	if err := database.QueryRow("SELECT COUNT(*) FROM functions").Scan(&totalFuncs); err != nil {
		return fmt.Errorf("count functions: %w", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM (
		SELECT file_path FROM file_summaries
		UNION SELECT file_path FROM functions
		UNION SELECT file_path FROM types
		UNION SELECT file_path FROM imports
		UNION SELECT file_path FROM documents
	)`).Scan(&totalFiles); err != nil {
		return fmt.Errorf("count files: %w", err)
	}
	h := ReadIndexHealth(database)
	return WriteStatus(dir, &Status{
		Healthy: h.Healthy, LastFullIndex: h.LastFullIndex, LastIncrementalIndex: h.LastIncrementalIndex,
		TotalFunctions: totalFuncs, TotalFiles: totalFiles, Version: version,
	})
}

// WriteIndexArtifacts refreshes every generated project artifact after a
// successful full index. All writes are attempted so one broken artifact does
// not prevent the others from being updated; the joined error remains visible
// to callers and health reporting.
func WriteIndexArtifacts(dir string, database *sql.DB, version string) error {
	var errs []error
	if err := WriteSummary(dir, database); err != nil {
		errs = append(errs, fmt.Errorf("write summary: %w", err))
	}
	if err := WriteArchitecture(dir, database); err != nil {
		errs = append(errs, fmt.Errorf("write architecture: %w", err))
	}
	if err := WriteIndexStatus(dir, database, version); err != nil {
		errs = append(errs, fmt.Errorf("write status: %w", err))
	}
	return errors.Join(errs...)
}
