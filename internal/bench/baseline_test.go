package bench

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNaiveBaseline_GrepAndRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nfunc AuthHandler() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package a\nfunc OtherThing() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tokens, err := NaiveBaseline(dir, []string{"AuthHandler"}, nil)
	if err != nil {
		t.Fatalf("NaiveBaseline: %v", err)
	}
	if tokens <= 0 {
		t.Fatalf("expected positive baseline tokens, got %d", tokens)
	}
}

func TestSkilledBaseline_NarrowerThanNaive(t *testing.T) {
	dir := t.TempDir()
	// Big file with one matching line near the top.
	content := "package a\nfunc AuthHandler() {}\n" + makeFiller(500)
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	naive, err := NaiveBaseline(dir, []string{"AuthHandler"}, nil)
	if err != nil {
		t.Fatalf("NaiveBaseline: %v", err)
	}
	skilled, err := SkilledBaseline(dir, []string{"AuthHandler"}, nil)
	if err != nil {
		t.Fatalf("SkilledBaseline: %v", err)
	}
	if skilled >= naive {
		t.Fatalf("skilled should be < naive (skilled=%d, naive=%d)", skilled, naive)
	}
}

func makeFiller(lines int) string {
	out := ""
	for i := 0; i < lines; i++ {
		out += "// filler line that is moderately long to make the file bigger than a match window\n"
	}
	return out
}
