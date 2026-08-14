package db

import (
	"database/sql/driver"
	"strings"

	sqlite "modernc.org/sqlite"
)

// SplitIdentifier breaks a symbol name into its constituent words, space
// separated, so FTS5 can match natural-language queries against code names.
//
// FTS5's unicode61 tokenizer treats "ResolverCache" as the single token
// "resolvercache", so the query "resolver cache" could not match it — while
// every symbol in a file called resolver_cache_test.go matched through the
// indexed file path and outranked it. Agents ask in words, so the words are
// indexed alongside the raw name.
//
//	ResolverCache   -> "Resolver Cache"
//	remove_file     -> "remove file"
//	HTTPServer      -> "HTTP Server"
//	parseGoFile     -> "parse Go File"
//	getURLFor2Files -> "get URL For 2 Files"
//
// Returns "" when the split adds nothing over the raw name (a single all-lower
// or all-upper word), so the column stays empty rather than duplicating it.
func SplitIdentifier(name string) string {
	words := identifierWords(name)
	if len(words) < 2 {
		return ""
	}
	return strings.Join(words, " ")
}

// identifierWords splits on non-alphanumeric separators and camelCase humps.
// An acronym run keeps its own word: "HTTPServer" -> ["HTTP", "Server"].
func identifierWords(name string) []string {
	var words []string
	var cur []rune

	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = nil
		}
	}

	runes := []rune(name)
	for i, r := range runes {
		switch {
		case !isAlphanumeric(r):
			// _, -, ., /, spaces: all separators.
			flush()
			continue
		case isUpper(r) && i > 0:
			prev := runes[i-1]
			// Start a new word at a lower->upper hump ("parseGo"), at a
			// digit->upper boundary ("v2Handler"), or at the last capital of an
			// acronym run followed by a lowercase ("HTTPServer" -> HTTP|Server).
			nextIsLower := i+1 < len(runes) && isLower(runes[i+1])
			if isLower(prev) || isDigit(prev) || (isUpper(prev) && nextIsLower) {
				flush()
			}
		case isDigit(r) && i > 0 && !isDigit(runes[i-1]):
			flush()
		}
		cur = append(cur, r)
	}
	flush()
	return words
}

func isAlphanumeric(r rune) bool { return isUpper(r) || isLower(r) || isDigit(r) }
func isUpper(r rune) bool        { return r >= 'A' && r <= 'Z' }
func isLower(r rune) bool        { return r >= 'a' && r <= 'z' }
func isDigit(r rune) bool        { return r >= '0' && r <= '9' }

// init registers SplitIdentifier as the SQL function split_identifier(name),
// so every INSERT derives name_parts at write time rather than relying on each
// caller to remember to pass it. Deterministic: the same name always yields the
// same split, which lets SQLite cache and use it in indexes.
func init() {
	if err := sqlite.RegisterDeterministicScalarFunction(
		"split_identifier", 1,
		func(ctx *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if len(args) != 1 || args[0] == nil {
				return nil, nil
			}
			name, ok := args[0].(string)
			if !ok {
				return nil, nil
			}
			parts := SplitIdentifier(name)
			if parts == "" {
				return nil, nil // NULL, not "", so FTS indexes nothing extra
			}
			return parts, nil
		},
	); err != nil {
		panic("register split_identifier: " + err.Error())
	}
}
