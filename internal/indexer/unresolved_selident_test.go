package indexer

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/maxcontext/max-context/pkg/treesitter"
)

// receiverOrigin classifies, for a sel-ident call x.M(), where x's binding comes
// from within its function — the signal that determines whether return-type
// inference (or another technique) would resolve it.
//
// Buckets (checked against the source of the enclosing function):
//   recv-or-param   x is the method receiver or a function parameter (already resolved today)
//   short-decl-call x := someCall(...)         — return-typed local (return-type inference target)
//   short-decl-other x := &T{} / literal / etc. (typed-local; resolved today if T is a bare type)
//   range-var       for x := range ...          — element-typed
//   assigned        x = ... (plain assignment)
//   field-or-global x is a struct field / package-level var / unknown
type selIdentSample struct {
	recv   string
	origin string
}

// TestUnresolvedSelIdentComposition drills into the dominant sel-ident bucket,
// classifying each x.M() call by the origin of x. This tells us which technique
// would convert the most unresolved edges. Informational; always passes.
func TestUnresolvedSelIdentComposition(t *testing.T) {
	ctx := context.Background()
	root := repoRoot(t)

	ignore, _ := NewIgnoreMatcher(root)
	sc := &Scanner{Root: root, Ignore: ignore}
	files, _ := sc.Scan()

	origin := map[string]int{}
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
		classifySelIdents(tree.RootNode(), content, origin)
		tree.Close()
	}

	total := 0
	for _, n := range origin {
		total += n
	}
	keys := make([]string, 0, len(origin))
	for k := range origin {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return origin[keys[i]] > origin[keys[j]] })

	t.Logf("sel-ident (x.M()) receiver origins (total=%d):", total)
	for _, k := range keys {
		t.Logf("  %-18s %5d  (%.1f%%)", k, origin[k], 100*float64(origin[k])/float64(total))
	}
}

// classifySelIdents finds every x.M() call and records the origin of x by
// scanning the enclosing function body's binding constructs.
func classifySelIdents(root *sitter.Node, src []byte, out map[string]int) {
	var visit func(n *sitter.Node, fnNode *sitter.Node)
	visit = func(n *sitter.Node, fnNode *sitter.Node) {
		switch n.Type() {
		case "function_declaration", "method_declaration", "func_literal":
			fnNode = n
		case "call_expression":
			fn := n.ChildByFieldName("function")
			if fn != nil && fn.Type() == "selector_expression" {
				op := fn.ChildByFieldName("operand")
				if op != nil && op.Type() == "identifier" {
					recv := op.Content(src)
					out[receiverOrigin(recv, fnNode, src)]++
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			visit(n.Child(i), fnNode)
		}
	}
	visit(root, nil)
}

// receiverOrigin inspects the enclosing function for how recv is bound.
func receiverOrigin(recv string, fnNode *sitter.Node, src []byte) string {
	if fnNode == nil {
		return "field-or-global"
	}
	// receiver or parameter?
	if recvNode := fnNode.ChildByFieldName("receiver"); recvNode != nil {
		if identInList(recvNode, recv, src) {
			return "recv-or-param"
		}
	}
	if params := fnNode.ChildByFieldName("parameters"); params != nil {
		if identInList(params, recv, src) {
			return "recv-or-param"
		}
	}
	// scan body for the strongest binding construct
	body := fnNode.ChildByFieldName("body")
	if body == nil {
		return "field-or-global"
	}
	origin := "field-or-global"
	var scan func(n *sitter.Node)
	scan = func(n *sitter.Node) {
		switch n.Type() {
		case "short_var_declaration":
			left := n.ChildByFieldName("left")
			right := n.ChildByFieldName("right")
			if left != nil && right != nil && identInList(left, recv, src) {
				if exprListHasCall(right) {
					origin = "short-decl-call"
				} else if origin == "field-or-global" {
					origin = "short-decl-other"
				}
			}
		case "range_clause":
			left := n.ChildByFieldName("left")
			if left != nil && identInList(left, recv, src) && origin == "field-or-global" {
				origin = "range-var"
			}
		case "assignment_statement":
			left := n.ChildByFieldName("left")
			if left != nil && identInList(left, recv, src) && origin == "field-or-global" {
				origin = "assigned"
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			scan(n.Child(i))
		}
	}
	scan(body)
	return origin
}

func identInList(n *sitter.Node, name string, src []byte) bool {
	if n.Type() == "identifier" && n.Content(src) == name {
		return true
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		if identInList(n.Child(i), name, src) {
			return true
		}
	}
	return false
}

func exprListHasCall(n *sitter.Node) bool {
	if n.Type() == "call_expression" {
		return true
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		if exprListHasCall(n.Child(i)) {
			return true
		}
	}
	return false
}
