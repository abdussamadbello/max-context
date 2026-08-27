package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/maxcontext/max-context/internal/artifacts"
	"github.com/maxcontext/max-context/internal/config"
	"github.com/maxcontext/max-context/internal/db"
)

func TestStartProjectWatcherRecordsStartupFailure(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{ProjectRoot: filepath.Join(dir, "missing")}
	if _, err := startProjectWatcher(context.Background(), cfg, database, make(chan string, 1)); err == nil {
		t.Fatal("watcher startup unexpectedly succeeded")
	}
	h := artifacts.ReadIndexHealth(database)
	if h.Healthy || h.FailedFiles != 1 {
		t.Fatalf("health after watcher failure = %+v", h)
	}
	var path string
	if err := database.QueryRow(`SELECT file_path FROM index_errors`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if path != artifacts.WatcherErrorKey {
		t.Fatalf("recorded error key = %q, want %q", path, artifacts.WatcherErrorKey)
	}
}
