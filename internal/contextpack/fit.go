package contextpack

import (
	"errors"
	"fmt"
)

var ErrBudgetTooSmall = errors.New("token budget is too small for response metadata")

// FitResult is the largest leading subset whose final serialized payload fits.
type FitResult struct {
	Kept       int
	TokensUsed int
	JSON       []byte
}

// FitJSONPrefix finds the largest prefix that fits budget. render must return a
// deterministic JSON payload for keep leading items and embed tokensUsed in the
// payload. Token accounting therefore covers the final serialized response,
// including metadata and JSON framing.
func FitJSONPrefix(counter *Counter, total, budget int, render func(keep, tokensUsed int) ([]byte, error)) (FitResult, error) {
	if counter == nil {
		return FitResult{}, fmt.Errorf("token counter is required")
	}
	if total < 0 {
		return FitResult{}, fmt.Errorf("total must be non-negative")
	}
	if budget <= 0 {
		return FitResult{}, fmt.Errorf("token budget must be positive")
	}

	baseJSON, baseTokens, err := renderWithStableCount(counter, 0, render)
	if err != nil {
		return FitResult{}, err
	}
	if baseTokens > budget {
		return FitResult{}, fmt.Errorf("%w: need at least %d tokens, got %d", ErrBudgetTooSmall, baseTokens, budget)
	}
	best := FitResult{Kept: 0, TokensUsed: baseTokens, JSON: baseJSON}
	low, high := 1, total
	for low <= high {
		mid := low + (high-low)/2
		payload, tokens, err := renderWithStableCount(counter, mid, render)
		if err != nil {
			return FitResult{}, err
		}
		if tokens <= budget {
			best = FitResult{Kept: mid, TokensUsed: tokens, JSON: payload}
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return best, nil
}

func renderWithStableCount(counter *Counter, keep int, render func(keep, tokensUsed int) ([]byte, error)) ([]byte, int, error) {
	tokens := 0
	for i := 0; i < 8; i++ {
		payload, err := render(keep, tokens)
		if err != nil {
			return nil, 0, err
		}
		actual, err := counter.Count(string(payload))
		if err != nil {
			return nil, 0, err
		}
		if actual == tokens {
			return payload, actual, nil
		}
		tokens = actual
	}
	return nil, 0, fmt.Errorf("token count did not stabilize")
}
