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

// CaptureGroup is one pattern match: capture name -> content text.
type CaptureGroup map[string]string

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
		group := make(CaptureGroup)
		for _, c := range m.Captures {
			name := q.CaptureNameForId(c.Index)
			group[name] = c.Node.Content(content)
		}
		if len(group) > 0 {
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
