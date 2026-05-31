package treesitter

import (
	"embed"
	"fmt"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
)

//go:embed queries/*.scm
var queryFS embed.FS

var (
	queryCache   = map[Lang][]byte{}
	queryCacheMu sync.Mutex
)

// NodePos is the start position of a captured node, 0-based (row, column) as
// reported by tree-sitter. Used to compute accurate source line numbers without
// re-scanning file content.
type NodePos struct {
	Row uint32
	Col uint32
}

// CaptureGroup is one pattern match. Text maps each capture name to the captured
// node's source text; Pos maps each capture name to that node's start position.
// Keeping positions here lets callers derive exact line numbers from the parse
// tree instead of searching file content (which is ambiguous for repeated text
// like a receiver identifier).
type CaptureGroup struct {
	Text map[string]string
	Pos  map[string]NodePos
}

// RunQuery runs the language's query file on the tree and returns all capture groups.
// content is the source bytes for extracting node text. Caller owns tree.
func RunQuery(tree *sitter.Tree, content []byte, lang Lang) ([]CaptureGroup, error) {
	qb, err := loadQuery(lang)
	if err != nil {
		return nil, err
	}
	langPtr, ok := langToSitter[lang]
	if !ok {
		return nil, nil
	}
	q, err := sitter.NewQuery(qb, langPtr)
	if err != nil {
		return nil, fmt.Errorf("new query: %w", err)
	}
	defer q.Close()

	root := tree.RootNode()
	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(q, root)

	var out []CaptureGroup
	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}
		m = qc.FilterPredicates(m, content)
		group := CaptureGroup{
			Text: make(map[string]string),
			Pos:  make(map[string]NodePos),
		}
		for _, c := range m.Captures {
			name := q.CaptureNameForId(c.Index)
			group.Text[name] = c.Node.Content(content)
			sp := c.Node.StartPoint()
			group.Pos[name] = NodePos{Row: sp.Row, Col: sp.Column}
		}
		if len(group.Text) > 0 {
			out = append(out, group)
		}
	}
	return out, nil
}

func loadQuery(lang Lang) ([]byte, error) {
	queryCacheMu.Lock()
	defer queryCacheMu.Unlock()
	if b, ok := queryCache[lang]; ok {
		return b, nil
	}
	name := string(lang) + ".scm"
	if lang == LangTSX {
		name = "typescript.scm"
	}
	if lang == LangJSX {
		name = "javascript.scm"
	}
	b, err := queryFS.ReadFile("queries/" + name)
	if err != nil {
		return nil, fmt.Errorf("read query %s: %w", name, err)
	}
	queryCache[lang] = b
	return b, nil
}
