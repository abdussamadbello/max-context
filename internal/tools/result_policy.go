package tools

import "strings"

const (
	answerStatusDefinitive = "definitive"
	answerStatusAmbiguous  = "ambiguous"
	answerStatusPartial    = "partial"

	actionAnswerNow           = "answer_now"
	actionCallQueryCodebase   = "call_query_codebase"
	actionRetryWithSuggestion = "retry_with_suggestion"
	actionInspectTopResult    = "inspect_top_result"
	actionInspectRelevantFile = "inspect_relevant_file"
	actionSwitchToolsOrAnswer = "switch_tools_or_answer"
	actionCallImpactOrChain   = "call_get_impact_or_get_call_chain"
	// actionNarrowScope tells the model the answer was capped and how to get a
	// smaller, sharper one rather than paging through a hub function.
	actionNarrowScope = "narrow_scope"
)

func definitionLabel(kind string) string {
	k := strings.ToLower(kind)
	switch {
	case strings.HasPrefix(k, "type/class"):
		return "class"
	case strings.HasPrefix(k, "type/interface"):
		return "interface"
	case strings.HasPrefix(k, "type/enum"):
		return "enum"
	case strings.HasPrefix(k, "type/struct"):
		return "struct"
	case strings.HasPrefix(k, "type/"):
		return "type"
	case k == "method":
		return "method"
	case k == "property":
		return "property"
	default:
		return "function"
	}
}

func definitionPriority(kind string) int {
	switch definitionLabel(kind) {
	case "class", "interface", "enum", "struct", "type":
		return 0
	case "function":
		return 1
	case "method", "property":
		return 2
	default:
		return 3
	}
}

func annotateDefinitionHit(h *definitionHit) {
	h.Label = definitionLabel(h.Kind)
}

func annotateSearchResult(r *searchResult) {
	r.Label = definitionLabel(r.Kind)
}

func canonicalDefinitionHit(hits []definitionHit) (definitionHit, []definitionHit, bool) {
	if len(hits) == 0 {
		return definitionHit{}, nil, false
	}
	bestPriority := definitionPriority(hits[0].Kind)
	bestIdx := 0
	ties := 1
	for i := 1; i < len(hits); i++ {
		p := definitionPriority(hits[i].Kind)
		if p < bestPriority {
			bestPriority = p
			bestIdx = i
			ties = 1
		} else if p == bestPriority {
			ties++
		}
	}
	if ties != 1 {
		return definitionHit{}, nil, false
	}
	canonical := hits[bestIdx]
	secondary := make([]definitionHit, 0, len(hits)-1)
	for i, h := range hits {
		if i != bestIdx {
			secondary = append(secondary, h)
		}
	}
	return canonical, secondary, true
}

func canonicalSearchResult(results []searchResult) (searchResult, []searchResult, bool) {
	if len(results) == 0 {
		return searchResult{}, nil, false
	}
	bestPriority := definitionPriority(results[0].Kind)
	bestIdx := 0
	ties := 1
	for i := 1; i < len(results); i++ {
		p := definitionPriority(results[i].Kind)
		if p < bestPriority {
			bestPriority = p
			bestIdx = i
			ties = 1
		} else if p == bestPriority {
			ties++
		}
	}
	if ties != 1 {
		return searchResult{}, nil, false
	}
	canonical := results[bestIdx]
	secondary := make([]searchResult, 0, len(results)-1)
	for i, r := range results {
		if i != bestIdx {
			secondary = append(secondary, r)
		}
	}
	return canonical, secondary, true
}

func detectedIntent(query string) string {
	q := strings.ToLower(query)
	switch {
	case strings.Contains(q, "where") && (strings.Contains(q, "defined") || strings.Contains(q, "definition")):
		return "definition"
	case strings.Contains(q, "depends on") || strings.Contains(q, "what uses") ||
		(strings.Contains(q, "where is") && strings.Contains(q, "used")) ||
		strings.Contains(q, "callers") || strings.Contains(q, "impact") ||
		strings.Contains(q, "breaks if"):
		return "impact"
	case strings.Contains(q, "edit") || strings.Contains(q, "change") ||
		strings.Contains(q, "add ") || strings.Contains(q, "remove "):
		return "edit_target"
	default:
		return "search"
	}
}

func formatLocation(file string, line int) string {
	if line > 0 {
		return file + ":" + itoa(line)
	}
	return file
}
