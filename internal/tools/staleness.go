package tools

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/maxcontext/max-context/internal/artifacts"
)

// stalenessInfo returns the index-health object attached to every tool response
// and a one-line warning when the index is unhealthy (failed files present).
// Defined once so all tools report staleness identically.
func stalenessInfo(database *sql.DB) (map[string]interface{}, string) {
	h := artifacts.ReadIndexHealth(database)
	obj := map[string]interface{}{
		"last_full_index":         rfc3339OrEmpty(h.LastFullIndex),
		"last_incremental_update": rfc3339OrEmpty(h.LastIncrementalIndex),
		"failed_files":            h.FailedFiles,
		"healthy":                 h.Healthy,
	}
	warning := ""
	if !h.Healthy {
		warning = fmt.Sprintf("Index may be stale: %d file(s) failed to index since the last clean build. "+
			"Results may be incomplete — run /reindex to rebuild.", h.FailedFiles)
	}
	return obj, warning
}

func rfc3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// attachStaleness adds the staleness object (and a warning, when unhealthy) to a
// JSON tool payload. Additive and backward compatible — existing fields are
// untouched.
func attachStaleness(payload map[string]interface{}, database *sql.DB) {
	obj, warning := stalenessInfo(database)
	payload["staleness"] = obj
	if warning != "" {
		payload["staleness_warning"] = warning
	}
}
