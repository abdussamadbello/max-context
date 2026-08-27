package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maxcontext/max-context/internal/artifacts"
	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/mcp"
)

func TestGetArchitectureFocusFiltersSubsystem(t *testing.T) {
	root := t.TempDir()
	database, err := db.Open(filepath.Join(root, ".max-context", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	q, err := db.PrepareQueries(database)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	for _, row := range []struct{ name, file string }{
		{"Index", "internal/indexer/indexer.go"},
		{"Resolve", "internal/indexer/resolver.go"},
		{"RegisterAll", "internal/tools/register.go"},
	} {
		if _, err := q.InsertFunction.Exec(row.name, row.file, 1, 5, "go", 1, "", "", row.name+"()"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.InsertImport.Exec("internal/indexer/indexer.go", "internal/db", ""); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".max-context")
	if err := artifacts.WriteSummary(dir, database); err != nil {
		t.Fatal(err)
	}
	if err := artifacts.WriteArchitecture(dir, database); err != nil {
		t.Fatal(err)
	}

	h := GetArchitectureHandler(database, root)
	resp, err := h(json.RawMessage(`{"focus":"indexer"}`))
	if err != nil {
		t.Fatal(err)
	}
	text := resp.([]mcp.ContentItem)[0].Text
	if !strings.Contains(text, "# Architecture focus: indexer") || !strings.Contains(text, "internal/indexer/indexer.go") {
		t.Fatalf("focused architecture missing indexer details:\n%s", text)
	}
	if strings.Contains(text, "internal/tools/register.go") {
		t.Fatalf("focused architecture leaked unrelated subsystem:\n%s", text)
	}
	if !strings.Contains(text, "internal/indexer/indexer.go -> internal/db") {
		t.Fatalf("focused architecture missing dependency:\n%s", text)
	}

	resp, err = h(json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	unfocused := resp.([]mcp.ContentItem)[0].Text
	arch, err := os.ReadFile(filepath.Join(dir, "architecture.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(unfocused, string(arch)) {
		t.Fatal("unfocused response should retain the precomputed architecture")
	}
}
