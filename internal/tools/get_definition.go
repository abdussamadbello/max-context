package tools

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/maxcontext/max-context/internal/mcp"
)

type getDefinitionArgs struct {
	Symbol string `json:"symbol"`
}

// definitionHit is one exact-name definition site.
type definitionHit struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Label     string `json:"label,omitempty"`
	File      string `json:"file"`
	Line      int    `json:"line,omitempty"`
	Canonical bool   `json:"canonical,omitempty"`
}

// GetDefinitionHandler answers "where is X defined?" with a single decisive
// result instead of a ranked list. This is the antidote to the query-loop the
// A/B experiment found (experiments/eval/FINDINGS.md): an exact-name lookup that
// returns one location gives the model grep's "found it, done" signal while
// keeping index precision. Falls back to listing candidates only when the name
// is ambiguous or absent.
func GetDefinitionHandler(database *sql.DB) mcp.ToolHandler {
	return func(args json.RawMessage) (interface{}, error) {
		var a getDefinitionArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &a); err != nil {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
			}
		}
		if a.Symbol == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "symbol required"}
		}

		hits := exactDefinitions(database, a.Symbol)

		payload := map[string]interface{}{"matches": hits, "total": len(hits)}
		switch len(hits) {
		case 0:
			// No exact match — point the model at fuzzy search rather than looping.
			payload["answer_status"] = answerStatusPartial
			payload["recommended_next_action"] = actionCallQueryCodebase
			payload["answer"] = fmt.Sprintf("No symbol named %q is defined in the index. "+
				"Use query_codebase for a fuzzy/keyword search.", a.Symbol)
		case 1:
			h := hits[0]
			h.Canonical = true
			payload["matches"] = []definitionHit{h}
			payload["canonical"] = h
			payload["answer_status"] = answerStatusDefinitive
			payload["recommended_next_action"] = actionAnswerNow
			payload["ambiguous"] = false
			payload["answer"] = fmt.Sprintf("%s (%s) is defined at %s. "+
				"This is the definitive location — answer now without further searches.",
				h.Name, h.Kind, formatLocation(h.File, h.Line))
		default:
			if canonical, secondary, ok := canonicalDefinitionHit(hits); ok {
				canonical.Canonical = true
				ordered := append([]definitionHit{canonical}, secondary...)
				payload["matches"] = ordered
				payload["canonical"] = canonical
				payload["secondary_matches"] = secondary
				payload["answer_status"] = answerStatusDefinitive
				payload["recommended_next_action"] = actionAnswerNow
				payload["ambiguous"] = false
				payload["overloaded"] = true
				payload["disambiguation_reason"] = fmt.Sprintf("%d exact matches found; %s definitions outrank same-named methods/properties for definition questions.", len(hits), canonical.Label)
				payload["answer"] = fmt.Sprintf("%s (%s) is canonically defined at %s. "+
					"Same-named secondary matches are listed separately; answer with the canonical file.",
					canonical.Name, canonical.Kind, formatLocation(canonical.File, canonical.Line))
			} else {
				payload["answer_status"] = answerStatusAmbiguous
				payload["recommended_next_action"] = actionInspectRelevantFile
				payload["ambiguous"] = true
				payload["ambiguity_reason"] = fmt.Sprintf("%d exact matches have equal priority; use the relevant module/file from matches.", len(hits))
				payload["answer"] = fmt.Sprintf("%q is defined in %d places (listed in matches). "+
					"Pick the one in the relevant module; no further keyword search needed.", a.Symbol, len(hits))
			}
		}
		attachStaleness(payload, database)
		b, _ := json.Marshal(payload)
		return []mcp.ContentItem{{Type: "text", Text: string(b)}}, nil
	}
}

// exactDefinitions returns every function/type whose name matches symbol exactly
// (case-insensitive), ordered for stable output.
func exactDefinitions(database *sql.DB, symbol string) []definitionHit {
	out := []definitionHit{}
	// Functions/methods.
	rows, err := database.Query(
		`SELECT name, file_path, start_line, kind FROM functions WHERE name = ? COLLATE NOCASE ORDER BY file_path, start_line`,
		symbol)
	if err == nil {
		for rows.Next() {
			var h definitionHit
			var kind sql.NullString
			if rows.Scan(&h.Name, &h.File, &h.Line, &kind) == nil {
				h.Kind = "function"
				if kind.Valid && kind.String == "method" {
					h.Kind = "method"
				}
				annotateDefinitionHit(&h)
				out = append(out, h)
			}
		}
		rows.Close()
	}
	// Types/classes/interfaces.
	trows, err := database.Query(
		`SELECT name, file_path, kind FROM types WHERE name = ? COLLATE NOCASE ORDER BY file_path`,
		symbol)
	if err == nil {
		for trows.Next() {
			var h definitionHit
			var kind string
			if trows.Scan(&h.Name, &h.File, &kind) == nil {
				h.Kind = "type/" + kind
				annotateDefinitionHit(&h)
				out = append(out, h)
			}
		}
		trows.Close()
	}
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := definitionPriority(out[i].Kind), definitionPriority(out[j].Kind)
		if pi != pj {
			return pi < pj
		}
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}
