// Package contextcompiler assembles a token-budgeted context package from the
// existing Max Context retrieval lanes. It is intentionally CLI-only while the
// product contract is evaluated; no MCP schema is registered here.
package contextcompiler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/maxcontext/max-context/internal/contextpack"
	"github.com/maxcontext/max-context/internal/db"
	"github.com/maxcontext/max-context/internal/mcp"
	"github.com/maxcontext/max-context/internal/tools"
)

type Options struct {
	Task         string
	TokenBudget  int
	Intent       string
	ChangedFiles []string
	MaxDepth     int
	Extensions   []string
	Include      []string
	Exclude      []string
	MaxFileSize  int64
}

type searchHit struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Snippet     string `json:"snippet"`
	Canonical   bool   `json:"canonical"`
	Relevance   float64
	QueryReason string
}

// Compile routes a task through existing deterministic tools, hydrates the
// strongest symbol hits with indexed source, and packs the resulting evidence
// under a hard final-response token budget.
func Compile(ctx context.Context, database *sql.DB, q *db.Queries, projectRoot string, opts Options) (contextpack.Package, []byte, error) {
	if err := ctx.Err(); err != nil {
		return contextpack.Package{}, nil, err
	}
	opts.Task = strings.TrimSpace(opts.Task)
	if opts.Task == "" {
		return contextpack.Package{}, nil, fmt.Errorf("task is required")
	}
	if opts.TokenBudget <= 0 {
		return contextpack.Package{}, nil, fmt.Errorf("token budget must be positive")
	}
	if opts.MaxDepth == 0 {
		opts.MaxDepth = 2
	}
	if opts.MaxDepth < 1 || opts.MaxDepth > 5 {
		return contextpack.Package{}, nil, fmt.Errorf("max depth must be between 1 and 5")
	}
	intent, err := resolveIntent(opts.Task, opts.Intent)
	if err != nil {
		return contextpack.Package{}, nil, err
	}

	hits, warnings, err := retrieveHits(database, q, projectRoot, opts.Task)
	if err != nil {
		return contextpack.Package{}, nil, err
	}
	candidates := make([]contextpack.Evidence, 0, len(hits)+24)
	for i, hit := range hits {
		candidates = append(candidates, evidenceForHit(database, hit, opts.Task, intent, i))
	}
	lexical, lexicalWarnings, err := lexicalEvidence(ctx, projectRoot, opts.Task, opts)
	if err != nil {
		return contextpack.Package{}, nil, fmt.Errorf("retrieve lexical context: %w", err)
	}
	candidates = append(candidates, lexical...)
	warnings = append(warnings, lexicalWarnings...)
	if len(hits) == 0 {
		warnings = append(warnings, "no indexed code or documentation matched the task")
	} else if !hasStrongTaskMatch(opts.Task, hits) {
		warnings = append(warnings, "no indexed evidence strongly matched the task; included results are lexical partials")
	}

	if intentNeedsGraph(intent) {
		graph, graphWarnings := graphEvidence(database, hits, opts.Task, opts.MaxDepth)
		candidates = append(candidates, graph...)
		warnings = append(warnings, graphWarnings...)
	}
	if intentNeedsImpact(intent) && (intent != IntentReview || len(opts.ChangedFiles) > 0 || taskRequestsCurrentDiff(opts.Task)) {
		impact, impactWarnings := impactEvidence(database, projectRoot, opts.Task, hits, lexical, opts.ChangedFiles, opts.MaxDepth)
		candidates = append(candidates, impact...)
		warnings = append(warnings, impactWarnings...)
	}
	if intentNeedsArchitecture(intent) {
		if arch, warning := architectureEvidence(database, projectRoot, hits); arch != nil {
			candidates = append(candidates, *arch)
		} else if warning != "" {
			warnings = append(warnings, warning)
		}
	}

	counter, err := contextpack.NewCounter()
	if err != nil {
		return contextpack.Package{}, nil, err
	}
	return contextpack.Pack(counter, opts.Task, intent, opts.TokenBudget, candidates, dedupeStrings(warnings))
}

func retrieveHits(database *sql.DB, q *db.Queries, projectRoot, task string) ([]searchHit, []string, error) {
	handler := tools.QueryCodebaseHandler(database, q, projectRoot)
	byID := map[string]searchHit{}
	var warnings []string
	queries := retrievalQueries(task)
	for queryIndex, query := range queries {
		args, _ := json.Marshal(map[string]interface{}{"query": query, "max_results": 10, "scope": "all"})
		text, err := callText(handler, args)
		if err != nil {
			if queryIndex == 0 {
				return nil, nil, fmt.Errorf("retrieve task context: %w", err)
			}
			warnings = append(warnings, fmt.Sprintf("supplemental query %q failed", query))
			continue
		}
		var response struct {
			Results          []searchHit `json:"results"`
			StalenessWarning string      `json:"staleness_warning"`
		}
		if err := json.Unmarshal([]byte(text), &response); err != nil {
			return nil, nil, fmt.Errorf("decode query response: %w", err)
		}
		if response.StalenessWarning != "" {
			warnings = append(warnings, response.StalenessWarning)
		}
		for rank, hit := range response.Results {
			id := fmt.Sprintf("%s:%s:%d:%s", hit.Kind, hit.File, hit.Line, hit.Name)
			relevance := 1.0 - float64(queryIndex)*0.08 - float64(rank)*0.025
			if relevance < 0.1 {
				relevance = 0.1
			}
			hit.Relevance = relevance
			hit.QueryReason = query
			if prior, ok := byID[id]; !ok || hit.Relevance > prior.Relevance {
				byID[id] = hit
			}
		}
	}
	hits := make([]searchHit, 0, len(byID))
	for _, hit := range byID {
		hits = append(hits, hit)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Canonical != hits[j].Canonical {
			return hits[i].Canonical
		}
		ai, aj := hitTaskAffinity(task, hits[i]), hitTaskAffinity(task, hits[j])
		if ai != aj {
			return ai > aj
		}
		if hits[i].Relevance != hits[j].Relevance {
			return hits[i].Relevance > hits[j].Relevance
		}
		if hits[i].File != hits[j].File {
			return hits[i].File < hits[j].File
		}
		return hits[i].Line < hits[j].Line
	})
	if len(hits) > 30 {
		hits = hits[:30]
	}
	return hits, warnings, nil
}

func evidenceForHit(database *sql.DB, hit searchHit, task, intent string, rank int) contextpack.Evidence {
	kind := evidenceKind(hit)
	content := hydrateHit(database, hit)
	if content == "" {
		content = hit.Snippet
	}
	content = clipContent(content, 3500)
	priority := 700
	confidence := "medium"
	affinity := hitTaskAffinity(task, hit)
	if hit.Canonical {
		priority, confidence, affinity = 1000, "high", 1
	} else if strings.HasPrefix(hit.Kind, "function") || hit.Kind == "method" || strings.HasPrefix(hit.Kind, "type/") {
		priority, confidence = 850, "high"
	}
	if affinity < .35 && !hit.Canonical {
		priority -= 200
		confidence = "low"
	}
	if isTestPath(hit.File) {
		kind = "test"
		if affinity >= .5 && (intent == IntentChange || intent == IntentDebug || intent == IntentTest) {
			priority = 900
		} else if intent == IntentReview {
			priority = 780
		}
	}
	return contextpack.Evidence{
		ID:         fmt.Sprintf("symbol:%s:%d:%s", hit.File, hit.Line, hit.Name),
		Kind:       kind,
		File:       hit.File,
		Line:       hit.Line,
		Symbol:     hit.Name,
		Content:    content,
		Reason:     fmt.Sprintf("matched task query %q", hit.QueryReason),
		Confidence: confidence,
		Relevance:  hit.Relevance*affinity - float64(rank)*0.001,
		Priority:   priority,
	}
}

func hydrateHit(database *sql.DB, hit searchHit) string {
	var content string
	switch {
	case hit.Kind == "document":
		_ = database.QueryRow(`SELECT content FROM documents WHERE file_path = ? ORDER BY id LIMIT 1`, hit.File).Scan(&content)
	case strings.HasPrefix(hit.Kind, "type/"):
		_ = database.QueryRow(`SELECT definition FROM types WHERE file_path = ? AND name = ? AND start_line = ? LIMIT 1`, hit.File, hit.Name, hit.Line).Scan(&content)
	default:
		_ = database.QueryRow(`SELECT code FROM functions WHERE file_path = ? AND name = ? AND start_line = ? LIMIT 1`, hit.File, hit.Name, hit.Line).Scan(&content)
	}
	return strings.TrimSpace(content)
}

func graphEvidence(database *sql.DB, hits []searchHit, task string, depth int) ([]contextpack.Evidence, []string) {
	handler := tools.GetCallChainHandler(database)
	var out []contextpack.Evidence
	var warnings []string
	seen := map[string]bool{}
	for _, hit := range hits {
		if len(out) == 3 {
			break
		}
		if seen[hit.Name] || hitTaskAffinity(task, hit) < .4 || (hit.Kind != "function" && hit.Kind != "method") {
			continue
		}
		seen[hit.Name] = true
		args, _ := json.Marshal(map[string]interface{}{
			"function_name": hit.Name,
			"direction":     "both",
			"depth":         depth,
			"max_results":   12,
		})
		text, err := callText(handler, args)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("call graph unavailable for %s", hit.Name))
			continue
		}
		out = append(out, contextpack.Evidence{
			ID:         "graph:" + hit.Name,
			Kind:       "call_graph",
			File:       hit.File,
			Line:       hit.Line,
			Symbol:     hit.Name,
			Content:    clipContent(text, 3500),
			Reason:     "nearest callers and callees for a task-relevant symbol",
			Confidence: "high",
			Relevance:  hit.Relevance,
			Priority:   800,
		})
	}
	return out, warnings
}

func impactEvidence(database *sql.DB, projectRoot, task string, hits []searchHit, lexical []contextpack.Evidence, changedFiles []string, depth int) ([]contextpack.Evidence, []string) {
	handler := tools.GetImpactHandler(db.NewSQLiteStore(database), projectRoot)
	argsMap := map[string]interface{}{"depth": depth, "direction": "callers", "include_tests": true}
	derivedAnchors := false
	if len(changedFiles) > 0 {
		argsMap["files"] = changedFiles
	} else if !taskRequestsCurrentDiff(task) {
		anchors := taskAnchorFiles(task, hits, lexical, 3)
		if len(anchors) == 0 {
			return nil, []string{"impact context skipped because no task-relevant file anchor was found"}
		}
		argsMap["files"] = anchors
		derivedAnchors = true
	}
	args, _ := json.Marshal(argsMap)
	text, err := callText(handler, args)
	if err != nil {
		return nil, []string{"git-aware impact context unavailable: " + err.Error()}
	}
	var response struct {
		Changed []struct {
			File    string   `json:"file"`
			Symbols []string `json:"symbols"`
		} `json:"changed"`
		Impacted []struct {
			File          string `json:"file"`
			Symbol        string `json:"symbol"`
			Line          int    `json:"line"`
			Depth         int    `json:"depth"`
			Via           string `json:"via"`
			ViaResolution string `json:"via_resolution"`
		} `json:"impacted"`
		UnresolvedFiles  []string `json:"unresolved_files"`
		StalenessWarning string   `json:"staleness_warning"`
	}
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		return nil, []string{"git-aware impact response could not be decoded"}
	}
	var out []contextpack.Evidence
	diffIsTaskAnchor := len(changedFiles) > 0 || derivedAnchors || taskRequestsCurrentDiff(task)
	skippedUnrelated := 0
	for _, changed := range response.Changed {
		relevance := taskAffinity(task, hits, changed.File, strings.Join(changed.Symbols, " "))
		if !diffIsTaskAnchor && relevance < .5 {
			skippedUnrelated++
			continue
		}
		priority := 350
		if diffIsTaskAnchor {
			relevance, priority = 1, 950
		} else if relevance >= .5 {
			priority = 880
		}
		kind := "changed_file"
		reason := "current diff or explicitly supplied changed file"
		if derivedAnchors {
			kind = "anchor_file"
			reason = "task-relevant file selected as an impact anchor"
		}
		out = append(out, contextpack.Evidence{
			ID:         "changed:" + changed.File,
			Kind:       kind,
			File:       changed.File,
			Content:    "Changed symbols: " + strings.Join(changed.Symbols, ", "),
			Reason:     reason,
			Confidence: "high",
			Relevance:  relevance,
			Priority:   priority,
		})
		for _, symbol := range changed.Symbols {
			if item := changedSymbolEvidence(database, task, changed.File, symbol, diffIsTaskAnchor); item != nil {
				out = append(out, *item)
			}
		}
	}
	for _, impacted := range response.Impacted {
		affinity := taskAffinity(task, hits, impacted.File, impacted.Symbol, impacted.Via)
		if !diffIsTaskAnchor && affinity < .5 {
			skippedUnrelated++
			continue
		}
		kind, priority := "impact", 300
		if affinity >= .5 || diffIsTaskAnchor {
			priority = 780
		}
		if isTestPath(impacted.File) {
			kind = "test"
			if affinity >= .5 || diffIsTaskAnchor {
				priority = 900
			} else {
				priority = 340
			}
		}
		out = append(out, contextpack.Evidence{
			ID:         fmt.Sprintf("symbol:%s:%d:%s", impacted.File, impacted.Line, impacted.Symbol),
			Kind:       kind,
			File:       impacted.File,
			Line:       impacted.Line,
			Symbol:     impacted.Symbol,
			Content:    fmt.Sprintf("%s is affected through %s at graph depth %d (resolution: %s).", impacted.Symbol, impacted.Via, impacted.Depth, impacted.ViaResolution),
			Reason:     "reachable from a changed symbol",
			Confidence: graphConfidence(impacted.ViaResolution),
			Relevance:  affinity / float64(impacted.Depth+1),
			Priority:   priority,
		})
	}
	var warnings []string
	if len(response.UnresolvedFiles) > 0 {
		warnings = append(warnings, fmt.Sprintf("%d changed file(s) had no indexed symbols", len(response.UnresolvedFiles)))
	}
	if skippedUnrelated > 0 {
		warnings = append(warnings, fmt.Sprintf("%d current-diff item(s) did not overlap the task and were omitted", skippedUnrelated))
	}
	if response.StalenessWarning != "" {
		warnings = append(warnings, response.StalenessWarning)
	}
	return out, warnings
}

func taskAnchorFiles(task string, hits []searchHit, lexical []contextpack.Evidence, limit int) []string {
	seen := map[string]bool{}
	var out []string
	add := func(file string) bool {
		file = filepath.ToSlash(strings.TrimSpace(file))
		if file == "" || seen[file] {
			return false
		}
		seen[file] = true
		out = append(out, file)
		return len(out) == limit
	}
	for _, item := range lexical {
		if item.Relevance >= .5 && add(item.File) {
			return out
		}
	}
	for _, hit := range hits {
		if hitTaskAffinity(task, hit) >= .5 && add(hit.File) {
			return out
		}
	}
	return out
}

func taskAffinity(task string, hits []searchHit, values ...string) float64 {
	combined := strings.ToLower(strings.Join(values, " "))
	for _, hit := range hits {
		if hitTaskAffinity(task, hit) < .5 {
			continue
		}
		if hit.File != "" && strings.Contains(combined, strings.ToLower(hit.File)) {
			return .9
		}
		if hit.Name != "" && strings.Contains(combined, strings.ToLower(hit.Name)) {
			return .85
		}
	}
	return termAffinity(task, combined)
}

func hitTaskAffinity(task string, hit searchHit) float64 {
	if hit.Canonical {
		return 1
	}
	return termAffinity(task, hit.Name, hit.File, hit.Snippet)
}

func termAffinity(task string, values ...string) float64 {
	terms := retrievalQueries(task)
	if len(terms) > 0 {
		terms = terms[1:]
	}
	if len(terms) == 0 {
		return .5
	}
	haystack := strings.ToLower(strings.Join(values, " "))
	compactHaystack := alphanumericLower(haystack)
	matches := 0
	for _, term := range terms {
		lower := strings.ToLower(term)
		if strings.Contains(haystack, lower) || strings.Contains(compactHaystack, alphanumericLower(lower)) {
			matches++
		}
	}
	if matches == 0 {
		return .15
	}
	// A candidate matching one concrete task anchor is already useful. Scaling
	// solely as matches/terms made richer task descriptions paradoxically lower
	// every score (one of eight anchors became 0.125 and looked unrelated).
	affinity := .45 + .55*float64(matches)/float64(len(terms))
	if affinity > 1 {
		return 1
	}
	return affinity
}

func hasStrongTaskMatch(task string, hits []searchHit) bool {
	for _, hit := range hits {
		if hitTaskAffinity(task, hit) >= .5 {
			return true
		}
	}
	return false
}

func taskRequestsCurrentDiff(task string) bool {
	task = strings.ToLower(task)
	return containsAny(task, "current diff", "current changes", "my changes", "changed files", "review changes", "review the diff")
}

func changedSymbolEvidence(database *sql.DB, task, file, symbol string, diffIsTaskAnchor bool) *contextpack.Evidence {
	var line int
	var content string
	err := database.QueryRow(`SELECT start_line, code FROM functions WHERE file_path = ? AND name = ? ORDER BY start_line LIMIT 1`, file, symbol).Scan(&line, &content)
	if err != nil {
		err = database.QueryRow(`SELECT start_line, definition FROM types WHERE file_path = ? AND name = ? ORDER BY start_line LIMIT 1`, file, symbol).Scan(&line, &content)
	}
	if err != nil || strings.TrimSpace(content) == "" {
		return nil
	}
	relevance := termAffinity(task, file, symbol, content)
	priority := 850
	if diffIsTaskAnchor {
		priority = 925
	}
	return &contextpack.Evidence{
		ID:         fmt.Sprintf("symbol:%s:%d:%s", file, line, symbol),
		Kind:       "definition",
		File:       file,
		Line:       line,
		Symbol:     symbol,
		Content:    clipContent(content, 3500),
		Reason:     "definition of a changed symbol",
		Confidence: "high",
		Relevance:  relevance,
		Priority:   priority,
	}
}

func alphanumericLower(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func architectureEvidence(database *sql.DB, projectRoot string, hits []searchHit) (*contextpack.Evidence, string) {
	focus := ""
	if len(hits) > 0 {
		focus = subsystemForPath(hits[0].File)
	}
	args, _ := json.Marshal(map[string]interface{}{"focus": focus})
	text, err := callText(tools.GetArchitectureHandler(database, projectRoot), args)
	if err != nil {
		return nil, "architecture context unavailable: " + err.Error()
	}
	return &contextpack.Evidence{
		ID:         "architecture:" + focus,
		Kind:       "architecture",
		File:       focus,
		Content:    clipContent(text, 3500),
		Reason:     "architecture around the highest-ranked task match",
		Confidence: "medium",
		Relevance:  .65,
		Priority:   600,
	}, ""
}

func callText(handler mcp.ToolHandler, args json.RawMessage) (string, error) {
	response, err := handler(args)
	if err != nil {
		return "", err
	}
	items, ok := response.([]mcp.ContentItem)
	if !ok || len(items) == 0 {
		return "", fmt.Errorf("tool returned no text content")
	}
	return items[0].Text, nil
}

func retrievalQueries(task string) []string {
	queries := []string{task}
	stop := map[string]bool{
		"accomplish": true, "add": true, "application": true, "before": true,
		"change": true, "code": true, "codebase": true, "common": true,
		"concrete": true, "debug": true, "describe": true, "deployment": true,
		"detail": true, "exact": true, "explain": true, "files": true,
		"find": true, "first": true, "fix": true, "focus": true,
		"functions": true, "identify": true, "implement": true, "initial": true,
		"locate": true, "modify": true, "new": true, "precisely": true,
		"relevant": true, "remove": true, "repository": true, "required": true,
		"review": true, "source": true, "staging": true, "task": true,
		"than": true, "that": true, "the": true, "their": true, "them": true,
		"this": true, "update": true, "what": true, "where": true, "with": true,
	}
	boost := map[string]int{
		"auth": 100, "authentication": 85, "authorization": 85,
		"config": 95, "configuration": 80, "dependency": 85,
		"error": 90, "failure": 90, "hardening": 70, "impact": 85,
		"jwt": 105, "secret": 105, "security": 75, "server": 90,
		"test": 90, "timeout": 105, "token": 95, "vulnerability": 80,
	}
	aliases := map[string]string{
		"authentication": "auth", "authorization": "auth",
		"configuration": "config", "configured": "config", "settings": "config",
		"dependencies": "dependency", "failures": "failure",
		"hardening": "timeout", "secrets": "secret", "tests": "test", "testing": "test",
		"tokens": "token", "vulnerabilities": "vulnerability",
	}
	type candidate struct {
		term  string
		score int
		pos   int
	}
	byTerm := map[string]candidate{}
	fields := strings.FieldsFunc(task, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
	for pos, field := range fields {
		term := strings.TrimSpace(field)
		lower := strings.ToLower(term)
		if len(lower) < 3 || stop[lower] {
			continue
		}
		score := 10 + boost[lower]
		if term != lower || strings.Contains(term, "_") {
			score += 25 // likely symbol/type identifier
		}
		if prior, ok := byTerm[lower]; ok {
			score = prior.score + 10 // repeated terms are stronger anchors
		}
		if prior, ok := byTerm[lower]; !ok || score > prior.score {
			byTerm[lower] = candidate{term: lower, score: score, pos: pos}
		}
		if alias := aliases[lower]; alias != "" {
			aliasScore := score + 20 + boost[alias]
			if prior, ok := byTerm[alias]; !ok || aliasScore > prior.score {
				byTerm[alias] = candidate{term: alias, score: aliasScore, pos: pos}
			}
		}
	}
	for original, alias := range aliases {
		if alias != original {
			if _, ok := byTerm[alias]; ok {
				delete(byTerm, original)
			}
		}
	}
	if secret, ok := byTerm["secret"]; ok {
		// In code, signing secrets are commonly named after the authentication
		// mechanism (JWT/auth) rather than containing the literal word "secret".
		for term, score := range map[string]int{"auth": 210, "jwt": 220} {
			if prior, exists := byTerm[term]; !exists || score > prior.score {
				byTerm[term] = candidate{term: term, score: score, pos: secret.pos}
			}
		}
	}
	ranked := make([]candidate, 0, len(byTerm))
	for _, item := range byTerm {
		ranked = append(ranked, item)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if ranked[i].pos != ranked[j].pos {
			return ranked[i].pos < ranked[j].pos
		}
		return ranked[i].term < ranked[j].term
	})
	for _, item := range ranked {
		queries = append(queries, item.term)
		if len(queries) == 9 {
			break
		}
	}
	return queries
}

func evidenceKind(hit searchHit) string {
	switch {
	case hit.Kind == "document":
		return "documentation"
	case strings.HasPrefix(hit.Kind, "type/"):
		return "type"
	case hit.Kind == "file":
		return "file"
	default:
		return "definition"
	}
}

func intentNeedsGraph(intent string) bool {
	return intent == IntentUnderstand || intent == IntentChange || intent == IntentDebug || intent == IntentImpact || intent == IntentReview
}

func intentNeedsImpact(intent string) bool {
	return intent == IntentChange || intent == IntentDebug || intent == IntentImpact || intent == IntentTest || intent == IntentReview
}

func intentNeedsArchitecture(intent string) bool {
	return intent == IntentUnderstand || intent == IntentReview
}

func graphConfidence(resolution string) string {
	switch resolution {
	case "same-file", "same-package", "receiver-typed":
		return "high"
	case "interface-dispatch":
		return "medium"
	default:
		return "low"
	}
}

func isTestPath(path string) bool {
	p := strings.ToLower(filepath.ToSlash(path))
	return strings.HasSuffix(p, "_test.go") || strings.Contains(p, "/test_") ||
		strings.HasSuffix(p, "_test.py") || strings.Contains(p, ".test.") || strings.Contains(p, ".spec.")
}

func subsystemForPath(path string) string {
	path = filepath.ToSlash(path)
	dir := filepath.ToSlash(filepath.Dir(path))
	if dir == "." {
		return strings.TrimSuffix(path, filepath.Ext(path))
	}
	parts := strings.Split(dir, "/")
	if len(parts) > 2 {
		return strings.Join(parts[:2], "/")
	}
	return dir
}

func clipContent(content string, maxBytes int) string {
	content = strings.TrimSpace(content)
	if len(content) <= maxBytes {
		return content
	}
	return strings.TrimSpace(content[:maxBytes]) + "\n[…truncated by context compiler…]"
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
