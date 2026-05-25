package indexer

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/maxcontext/max-context/pkg/treesitter"
)

// FuncRecord is a function/method to be stored in the index.
type FuncRecord struct {
	Name       string
	FilePath   string
	StartLine  int
	EndLine    int
	Language   string
	Exported   bool
	Code       string
	Docstring  string
	Signature  string
}

// CallRecord is a call edge (caller -> callee).
type CallRecord struct {
	CalleeName string
	FilePath   string
	Line       int
	CallerID   int64
}

// TypeRecord is a type declaration.
type TypeRecord struct {
	Name      string
	FilePath  string
	Kind      string
	Definition string
	Exported  bool
}

// ImportRecord is an import relationship.
type ImportRecord struct {
	FilePath       string
	ImportedPath   string
	ImportedSymbols string
}

// ParseResult holds all extracted records for one file.
type ParseResult struct {
	Functions []FuncRecord
	Calls     []CallRecord
	Types     []TypeRecord
	Imports   []ImportRecord
}

// ParseFile parses the file content and returns extracted symbols and edges.
func ParseFile(ctx context.Context, filePath string, content []byte) (*ParseResult, error) {
	lang, ok := treesitter.LanguageForPath(filePath)
	if !ok {
		return &ParseResult{}, nil
	}

	tree, err := treesitter.Parse(ctx, content, lang)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	groups, err := treesitter.RunQuery(tree, content, lang)
	if err != nil {
		return nil, err
	}

	res := &ParseResult{}
	lineByNode := make(map[string][2]int)

	for _, g := range groups {
		if name, ok := g["name"]; ok && name != "" {
			if _, hasBody := g["body"]; hasBody {
				start, end := lineRangeFromGroup(g, content)
				exported := isExported(name)
				res.Functions = append(res.Functions, FuncRecord{
					Name:      name,
					FilePath:  filepath.ToSlash(filePath),
					StartLine: start,
					EndLine:   end,
					Language:  string(lang),
					Exported:  exported,
					Code:      truncate(g["body"], 500),
					Docstring: "",
					Signature: name,
				})
				lineByNode[name] = [2]int{start, end}
			}
		}
		if callee, ok := g["callee"]; ok && callee != "" {
			line := firstLineFromGroup(g, content)
			res.Calls = append(res.Calls, CallRecord{
				CalleeName: callee,
				FilePath:   filepath.ToSlash(filePath),
				Line:       line,
			})
		}
		if name, ok := g["name"]; ok && name != "" {
			if def, hasDef := g["definition"]; hasDef && def != "" {
				kind := "type"
				res.Types = append(res.Types, TypeRecord{
					Name:       name,
					FilePath:   filepath.ToSlash(filePath),
					Kind:       kind,
					Definition: truncate(def, 300),
					Exported:   isExported(name),
				})
			}
		}
		if path, ok := g["path"]; ok && path != "" {
			path = strings.Trim(path, "\"'`")
			symbols := g["symbol"]
			res.Imports = append(res.Imports, ImportRecord{
				FilePath:       filepath.ToSlash(filePath),
				ImportedPath:   path,
				ImportedSymbols: symbols,
			})
		}
	}

	return res, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func isExported(name string) bool {
	if name == "" {
		return false
	}
	c := name[0]
	return c >= 'A' && c <= 'Z'
}

func firstLineFromGroup(g treesitter.CaptureGroup, content []byte) int {
	for _, v := range g {
		if v != "" {
			if idx := strings.Index(string(content), v); idx >= 0 {
				return 1 + strings.Count(string(content[:idx]), "\n")
			}
			return 1
		}
	}
	return 1
}

func lineRangeFromGroup(g treesitter.CaptureGroup, content []byte) (start, end int) {
	start, end = 1, 1
	for _, v := range g {
		if v == "" {
			continue
		}
		idx := strings.Index(string(content), v)
		if idx >= 0 {
			start = 1 + strings.Count(string(content[:idx]), "\n")
			end = start + strings.Count(v, "\n")
			if end < start {
				end = start
			}
			return start, end
		}
	}
	return 1, 1
}
