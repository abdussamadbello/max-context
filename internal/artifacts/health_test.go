package artifacts

import (
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/db"
)

func TestClearErrorsAfterFullIndexPreservesWatcherFailure(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	RecordIndexError(database, WatcherErrorKey, "overflow")
	RecordIndexError(database, "broken.go", "parse failed")
	RecordIndexError(database, "", "full failed")

	ClearErrorsAfterFullIndex(database)

	var key string
	if err := database.QueryRow(`SELECT file_path FROM index_errors`).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if key != WatcherErrorKey {
		t.Fatalf("remaining error = %q, want watcher sentinel", key)
	}
}
