package contextpack

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestPackHonorsFinalSerializedBudget(t *testing.T) {
	counter, err := NewCounter()
	if err != nil {
		t.Fatal(err)
	}
	candidates := []Evidence{
		{ID: "large", Kind: "architecture", Content: strings.Repeat("large context ", 300), Reason: "overview", Confidence: "medium", Relevance: .8, Priority: 700},
		{ID: "anchor", Kind: "definition", File: "auth.go", Line: 10, Symbol: "RefreshToken", Content: "func RefreshToken() {}", Reason: "exact task anchor", Confidence: "high", Relevance: 1, Priority: 1000},
		{ID: "test", Kind: "test", File: "auth_test.go", Line: 20, Content: "func TestRefreshToken() {}", Reason: "related test", Confidence: "high", Relevance: .9, Priority: 800},
	}
	pkg, payload, err := Pack(counter, "change refresh token", "change", 220, candidates, nil)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := counter.Count(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	if actual != pkg.TokensUsed || actual > pkg.TokenBudget {
		t.Fatalf("tokens actual=%d reported=%d budget=%d", actual, pkg.TokensUsed, pkg.TokenBudget)
	}
	if len(pkg.Evidence) == 0 || pkg.Evidence[0].ID != "anchor" {
		t.Fatalf("priority anchor was not retained: %+v", pkg.Evidence)
	}
	if pkg.Complete || pkg.Omitted.Total == 0 {
		t.Fatalf("expected visible omissions: %+v", pkg)
	}
	var decoded Package
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("invalid package JSON: %v", err)
	}
}

func TestPackCompleteWithGenerousBudgetAndDeduplicates(t *testing.T) {
	counter, _ := NewCounter()
	item := Evidence{ID: "same", Kind: "definition", Content: "func F() {}", Reason: "anchor", Confidence: "high", Relevance: 1, Priority: 1000}
	lower := item
	lower.Content = "less useful duplicate"
	lower.Priority = 100
	pkg, payload, err := Pack(counter, "find F", "locate", 1000, []Evidence{lower, item}, []string{"fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if !pkg.Complete || len(pkg.Evidence) != 1 || pkg.Omitted.Total != 0 {
		t.Fatalf("unexpected package: %+v", pkg)
	}
	if pkg.Evidence[0].Content != item.Content {
		t.Fatalf("deduplication kept lower-priority evidence: %+v", pkg.Evidence[0])
	}
	actual, _ := counter.Count(string(payload))
	if actual != pkg.TokensUsed {
		t.Fatalf("actual=%d reported=%d", actual, pkg.TokensUsed)
	}
}

func TestPackRejectsBudgetBelowMetadata(t *testing.T) {
	counter, _ := NewCounter()
	_, _, err := Pack(counter, strings.Repeat("task ", 100), "understand", 10, nil, nil)
	if !errors.Is(err, ErrBudgetTooSmall) {
		t.Fatalf("error = %v, want ErrBudgetTooSmall", err)
	}
}

func TestFitJSONPrefixUsesFinalPayloadSize(t *testing.T) {
	counter, _ := NewCounter()
	items := []string{"one", "two", strings.Repeat("three ", 200)}
	result, err := FitJSONPrefix(counter, len(items), 30, func(keep, tokens int) ([]byte, error) {
		return json.Marshal(struct {
			Items  []string `json:"items"`
			Tokens int      `json:"tokens_used"`
		}{Items: items[:keep], Tokens: tokens})
	})
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := counter.Count(string(result.JSON))
	if result.Kept != 2 || actual != result.TokensUsed || actual > 30 {
		t.Fatalf("fit = %+v actual=%d", result, actual)
	}
}

func TestPackAppliesDiversityCaps(t *testing.T) {
	counter, _ := NewCounter()
	var candidates []Evidence
	for i := 0; i < 10; i++ {
		candidates = append(candidates, Evidence{
			ID: fmt.Sprintf("item-%d", i), Kind: "definition", File: "hub.go",
			Content: "func F() {}", Reason: "match", Confidence: "high", Relevance: 1, Priority: 800,
		})
	}
	pkg, _, err := Pack(counter, "inspect hub", "understand", 5000, candidates, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Evidence) != maxEvidencePerFile || pkg.Complete || pkg.Omitted.Total != 7 {
		t.Fatalf("diversity cap not reflected in package: %+v", pkg)
	}
}
