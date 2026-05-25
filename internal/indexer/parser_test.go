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
