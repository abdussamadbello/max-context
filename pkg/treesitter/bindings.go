package treesitter

import (
	"context"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/ruby"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/swift"
	tsx "github.com/smacker/go-tree-sitter/typescript/tsx"
	ts "github.com/smacker/go-tree-sitter/typescript/typescript"
)

// Lang identifies a tree-sitter language.
type Lang string

const (
	LangTypeScript Lang = "typescript"
	LangTSX        Lang = "tsx"
	LangJavaScript Lang = "javascript"
	LangJSX        Lang = "jsx"
	LangPython     Lang = "python"
	LangGo         Lang = "go"
	LangRust       Lang = "rust"
	LangJava       Lang = "java"
	LangC          Lang = "c"
	LangCpp        Lang = "cpp"
	LangRuby       Lang = "ruby"
	LangSwift      Lang = "swift"
)

// LanguageForExt returns the language and true if the file extension is supported, otherwise ("", false).
func LanguageForExt(ext string) (Lang, bool) {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "ts":
		return LangTypeScript, true
	case "tsx":
		return LangTSX, true
	case "js", "mjs", "cjs":
		return LangJavaScript, true
	case "jsx":
		return LangJSX, true
	case "py", "pyi":
		return LangPython, true
	case "go":
		return LangGo, true
	case "rs":
		return LangRust, true
	case "java":
		return LangJava, true
	case "c", "h":
		return LangC, true
	case "cc", "cpp", "cxx", "hh", "hpp", "hxx":
		return LangCpp, true
	case "rb":
		return LangRuby, true
	case "swift":
		return LangSwift, true
	default:
		return "", false
	}
}

// LanguageForPath returns the language for the given file path (based on extension).
func LanguageForPath(path string) (Lang, bool) {
	return LanguageForExt(filepath.Ext(path))
}

var langToSitter = map[Lang]*sitter.Language{
	LangTypeScript: ts.GetLanguage(),
	LangTSX:        tsx.GetLanguage(),
	LangJavaScript: javascript.GetLanguage(),
	LangJSX:        javascript.GetLanguage(),
	LangPython:     python.GetLanguage(),
	LangGo:         golang.GetLanguage(),
	LangRust:       rust.GetLanguage(),
	LangJava:       java.GetLanguage(),
	LangC:          c.GetLanguage(),
	LangCpp:        cpp.GetLanguage(),
	LangRuby:       ruby.GetLanguage(),
	LangSwift:      swift.GetLanguage(),
}

// Parse parses content with the given language and returns the syntax tree.
// Caller must call tree.Close() when done.
func Parse(ctx context.Context, content []byte, lang Lang) (*sitter.Tree, error) {
	sitterLang, ok := langToSitter[lang]
	if !ok {
		return nil, nil
	}
	p := sitter.NewParser()
	defer p.Close()
	p.SetLanguage(sitterLang)
	return p.ParseCtx(ctx, nil, content)
}

// ExtensionsForLang returns the file extensions (with leading dot) for a
// language name as written in .max-context/config.json "languages". Accepts
// canonical names and common aliases, case-insensitively; unknown names
// return nil.
func ExtensionsForLang(name string) []string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "typescript", "ts":
		return []string{".ts", ".tsx"}
	case "tsx":
		return []string{".tsx"}
	case "javascript", "js":
		return []string{".js", ".jsx", ".mjs", ".cjs"}
	case "jsx":
		return []string{".jsx"}
	case "python", "py":
		return []string{".py", ".pyi"}
	case "go", "golang":
		return []string{".go"}
	case "rust", "rs":
		return []string{".rs"}
	case "java":
		return []string{".java"}
	case "c":
		return []string{".c", ".h"}
	case "cpp", "c++", "cxx":
		return []string{".cc", ".cpp", ".cxx", ".hh", ".hpp", ".hxx"}
	case "ruby", "rb":
		return []string{".rb"}
	case "swift":
		return []string{".swift"}
	default:
		return nil
	}
}

// SupportedExtensions returns a list of file extensions that can be parsed.
func SupportedExtensions() []string {
	return []string{
		".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".py", ".pyi", ".go", ".rs",
		".java", ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx", ".rb", ".swift",
	}
}
