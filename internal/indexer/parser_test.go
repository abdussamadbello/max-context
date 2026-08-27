package indexer

import (
	"context"
	"testing"
)

func TestParseFile_Empty(t *testing.T) {
	ctx := context.Background()
	res, err := ParseFile(ctx, "x.go", []byte(""))
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestParseFile_UnwrapsOuterMarkdownFence(t *testing.T) {
	res, err := ParseFile(context.Background(), "main.go", []byte("```go\npackage main\nfunc Serve() {}\n```\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Functions) != 1 || res.Functions[0].Name != "Serve" {
		t.Fatalf("functions = %+v", res.Functions)
	}
	if res.Functions[0].StartLine != 3 {
		t.Fatalf("line = %d, want 3", res.Functions[0].StartLine)
	}
}

func TestUnwrapOuterMarkdownFenceLeavesNormalSourceUnchanged(t *testing.T) {
	src := []byte("package main\n// ```go\nfunc Serve() {}\n")
	got := unwrapOuterMarkdownFence(src)
	if string(got) != string(src) {
		t.Fatalf("normal source changed:\n%s", got)
	}
}
