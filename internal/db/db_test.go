package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestOpenMigrateAndQueries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	q, err := PrepareQueries(db)
	if err != nil {
		t.Fatalf("PrepareQueries: %v", err)
	}
	defer q.Close()

	// Rebuild empty FTS (no rows)
	if err := RebuildAllFTS(db); err != nil {
		t.Fatalf("RebuildAllFTS: %v", err)
	}
}

func TestOpenCreatesDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "index.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("parent dir not created: %v", err)
	}
}

func TestOpenAppliesSafetyPragmasToEveryPooledConnection(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "pool.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(4)

	ctx := context.Background()
	var conns []*sql.Conn
	for i := 0; i < 4; i++ {
		conn, err := database.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, conn)
	}
	defer func() {
		for _, conn := range conns {
			conn.Close()
		}
	}()
	for i, conn := range conns {
		var foreignKeys, busyTimeout int
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
			t.Fatal(err)
		}
		if foreignKeys != 1 || busyTimeout != 5000 {
			t.Errorf("connection %d pragmas: foreign_keys=%d busy_timeout=%d", i, foreignKeys, busyTimeout)
		}
	}
}

func TestOpenConcurrentProcessesWaitForWALSetup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	seed, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	if err := Migrate(seed); err != nil {
		t.Fatal(err)
	}

	const workers = 16
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			database, err := Open(path)
			if err == nil {
				err = database.Ping()
				_ = database.Close()
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Open: %v", err)
		}
	}
}
