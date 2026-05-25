package treesitter

import (
	"context"
	"testing"
)

func TestLanguageForExt(t *testing.T) {
	if lang, ok := LanguageForExt(".ts"); !ok || lang != LangTypeScript {
		t.Fatalf("got %v %v", lang, ok)
	}
	if lang, ok := LanguageForExt(".go"); !ok || lang != LangGo {
		t.Fatalf("got %v %v", lang, ok)
	}
	if _, ok := LanguageForExt(".xyz"); ok {
		t.Fatal("expected false for unknown ext")
	}
}

func TestParse(t *testing.T) {
	ctx := context.Background()
	content := []byte("func main() {}")
	tree, err := Parse(ctx, content, LangGo)
	if err != nil {
		t.Fatal(err)
	}
	if tree != nil {
		defer tree.Close()
		if tree.RootNode().Type() != "source_file" && tree.RootNode().Type() != "source" {
			t.Log("root type:", tree.RootNode().Type())
		}
	}
}

// TestParseAndQuery_NewLangs ensures Phase 3 languages load and run queries without invalid node types.
func TestParseAndQuery_NewLangs(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		lang    Lang
		source  string
		ext     string
	}{
		{LangJava, "class Foo { void bar() {} }", ".java"},
		{LangC, "void foo() {}", ".c"},
		{LangCpp, "void foo() {}", ".cpp"},
		{LangRuby, "def foo; end", ".rb"},
		{LangSwift, "func foo() {}", ".swift"},
	}
	for _, tt := range tests {
		t.Run(string(tt.lang), func(t *testing.T) {
			tree, err := Parse(ctx, []byte(tt.source), tt.lang)
			if err != nil {
				t.Fatal(err)
			}
			if tree == nil {
				t.Fatal("expected tree")
			}
			defer tree.Close()
			_, err = RunQuery(tree, []byte(tt.source), tt.lang)
			if err != nil {
				t.Fatalf("RunQuery: %v", err)
			}
		})
	}
}
