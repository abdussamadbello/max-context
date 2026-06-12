package artifacts

import (
	"database/sql"
	"time"
)

// IndexHealth is the queryable staleness/health summary surfaced in every MCP
// tool response and by --status. It is derived entirely from the index DB
// (_meta timestamps + the index_errors table) so any code holding a *sql.DB can
// read it without locating the .max-context directory.
type IndexHealth struct {
	LastFullIndex        time.Time
	LastIncrementalIndex time.Time
	FailedFiles          int
	Healthy              bool
}

const (
	metaLastFullIndex        = "last_full_index"
	metaLastIncrementalIndex = "last_incremental_index"
	// FullIndexErrorKey is the index_errors.file_path sentinel for a whole-repo
	// (full) index failure, which has no single originating file.
	FullIndexErrorKey = "<full-index>"
)

// ReadIndexHealth returns the current index health. Missing tables/keys (older
// DBs) read as zero/healthy rather than erroring.
func ReadIndexHealth(database *sql.DB) IndexHealth {
	h := IndexHealth{Healthy: true}
	h.LastFullIndex = readMetaTime(database, metaLastFullIndex)
	h.LastIncrementalIndex = readMetaTime(database, metaLastIncrementalIndex)
	if err := database.QueryRow("SELECT COUNT(*) FROM index_errors").Scan(&h.FailedFiles); err != nil {
		h.FailedFiles = 0 // table absent on a pre-v7 DB -> treat as no failures
	}
	h.Healthy = h.FailedFiles == 0
	return h
}

func readMetaTime(database *sql.DB, key string) time.Time {
	var s sql.NullString
	if err := database.QueryRow("SELECT value FROM _meta WHERE key = ?", key).Scan(&s); err != nil || !s.Valid {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s.String)
	if err != nil {
		return time.Time{}
	}
	return t
}

func writeMetaTime(database *sql.DB, key string, t time.Time) {
	_, _ = database.Exec("INSERT OR REPLACE INTO _meta (key, value) VALUES (?, ?)", key, t.UTC().Format(time.RFC3339))
}

// SetLastFullIndex records the timestamp of a successful full index.
func SetLastFullIndex(database *sql.DB, t time.Time) { writeMetaTime(database, metaLastFullIndex, t) }

// SetLastIncrementalIndex records the timestamp of a successful incremental update.
func SetLastIncrementalIndex(database *sql.DB, t time.Time) {
	writeMetaTime(database, metaLastIncrementalIndex, t)
}

// RecordIndexError records (or replaces) the failure for a file so it surfaces
// in staleness. An empty filePath is stored under the FullIndexErrorKey sentinel.
func RecordIndexError(database *sql.DB, filePath, msg string) {
	if filePath == "" {
		filePath = FullIndexErrorKey
	}
	_, _ = database.Exec(
		"INSERT OR REPLACE INTO index_errors (file_path, error, timestamp) VALUES (?, ?, ?)",
		filePath, msg, time.Now().Unix(),
	)
}

// ClearIndexError removes any recorded failure for a file after it indexes cleanly.
func ClearIndexError(database *sql.DB, filePath string) {
	_, _ = database.Exec("DELETE FROM index_errors WHERE file_path = ?", filePath)
}

// ClearAllIndexErrors clears every recorded failure (after a successful full reindex).
func ClearAllIndexErrors(database *sql.DB) {
	_, _ = database.Exec("DELETE FROM index_errors")
}
