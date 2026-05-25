package artifacts

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteReadStatus(t *testing.T) {
	dir := t.TempDir()
	s := &Status{
		Healthy:        true,
		LastFullIndex:  time.Now(),
		TotalFunctions: 10,
		TotalFiles:     5,
		Version:        "0.1.0",
	}
	if err := WriteStatus(dir, s); err != nil {
		t.Fatal(err)
	}
	read, err := ReadStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if read.Healthy != s.Healthy || read.TotalFunctions != s.TotalFunctions {
		t.Fatalf("got %+v", read)
	}
}

func TestWriteStatusAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "status.json")
	tmp := p + ".tmp"
	s := &Status{Healthy: true}
	WriteStatus(dir, s)
	if _, err := os.Stat(tmp); err == nil {
		t.Fatal("temp file should not exist after rename")
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("status.json should exist")
	}
}
