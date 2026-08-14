package indexer

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/maxcontext/max-context/pkg/treesitter"
	sitter "github.com/smacker/go-tree-sitter"
)

// classifyCallExpr buckets a Go call_expression by the SHAPE of its function
// expression — independent of the indexer. This reveals both the calls the
// resolver handles and the ones the current go.scm never even captures.
//
// Buckets:
//
//	bare-call        f()                       — identifier
//	sel-ident        x.M()                     — selector on a bare identifier (we capture this)
//	sel-field        a.b.M()                   — selector on another selector (field/pkg-chain receiver)
//	sel-chain        f().M()                   — selector on a call result (return-typed)
//	sel-index        a[i].M()                  — selector on an index/slice expr
//	sel-paren-other  (x).M(), x.y().z.M()      — any other selector operand
//	other            type conversions, etc.
func classifyCallExpr(n *sitter.Node, src []byte) string {
	fn := n.ChildByFieldName("function")
	if fn == nil {
		return "other"
	}
	switch fn.Type() {
	case "identifier":
		return "bare-call"
	case "selector_expression":
		operand := fn.ChildByFieldName("operand")
		if operand == nil {
			return "sel-paren-other"
		}
		switch operand.Type() {
		case "identifier":
			return "sel-ident"
		case "selector_expression":
			return "sel-field"
		case "call_expression":
			return "sel-chain"
		case "index_expression":
			return "sel-index"
		default:
			return "sel-paren-other"
		}
	default:
		return "other"
	}
}

func walkCalls(n *sitter.Node, src []byte, counts map[string]int) {
	if n.Type() == "call_expression" {
		counts[classifyCallExpr(n, src)]++
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		walkCalls(n.Child(i), src, counts)
	}
}

// TestUnresolvedComposition classifies every Go call expression in the self-repo
// by syntactic shape, so we can see which inference would convert the most
// currently-unresolved (or uncaptured) edges. Informational; always passes.
func TestUnresolvedComposition(t *testing.T) {
	ctx := context.Background()
	root := repoRoot(t)

	ignore, _ := NewIgnoreMatcher(root)
	sc := &Scanner{Root: root, Ignore: ignore}
	files, err := sc.Scan()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	counts := map[string]int{}
	goFiles := 0
	for _, rel := range files {
		if filepath.Ext(rel) != ".go" || strings.HasSuffix(rel, "_test.go") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		tree, err := treesitter.Parse(ctx, content, treesitter.LangGo)
		if err != nil || tree == nil {
			continue
		}
		walkCalls(tree.RootNode(), content, counts)
		tree.Close()
		goFiles++
	}

	total := 0
	for _, n := range counts {
		total += n
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return counts[keys[i]] > counts[keys[j]] })

	t.Logf("Go call-expression shapes across %d non-test files (total=%d calls):", goFiles, total)
	for _, k := range keys {
		t.Logf("  %-16s %5d  (%.1f%%)  %s", k, counts[k], 100*float64(counts[k])/float64(total), shapeHint(k))
	}
	if total == 0 {
		t.Fatal("no Go calls found")
	}
}

func shapeHint(bucket string) string {
	switch bucket {
	case "bare-call":
		return "f() — same-package/builtin (mostly resolved today)"
	case "sel-ident":
		return "x.M() — captured; resolved if x is import/param/typed-local, else unresolved"
	case "sel-field":
		return "a.b.M() — NOT captured today; needs struct-field/pkg typing"
	case "sel-chain":
		return "f().M() — NOT captured; needs return-type inference"
	case "sel-index":
		return "a[i].M() — NOT captured; needs element-type inference"
	case "sel-paren-other":
		return "other selector receiver — hard"
	default:
		return "type conversions etc."
	}
}
