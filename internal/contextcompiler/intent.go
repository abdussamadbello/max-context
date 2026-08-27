package contextcompiler

import (
	"fmt"
	"strings"
)

const (
	IntentAuto       = "auto"
	IntentLocate     = "locate"
	IntentUnderstand = "understand"
	IntentChange     = "change"
	IntentDebug      = "debug"
	IntentImpact     = "impact"
	IntentTest       = "test"
	IntentReview     = "review"
)

var validIntents = map[string]bool{
	IntentAuto: true, IntentLocate: true, IntentUnderstand: true,
	IntentChange: true, IntentDebug: true, IntentImpact: true,
	IntentTest: true, IntentReview: true,
}

func resolveIntent(task, requested string) (string, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		requested = IntentAuto
	}
	if !validIntents[requested] {
		return "", fmt.Errorf("intent must be one of: auto, locate, understand, change, debug, impact, test, review")
	}
	if requested != IntentAuto {
		return requested, nil
	}

	t := strings.ToLower(task)
	switch {
	case containsAny(t, "review", "audit", "security", "vulnerability"):
		return IntentReview, nil
	case containsAny(t, "impact", "affected", "what breaks", "blast radius", "depends on"):
		return IntentImpact, nil
	case containsAny(t, "debug", "bug", "error", "failure", "fails", "exception", "panic", "fix"):
		return IntentDebug, nil
	case containsAny(t, "test", "coverage", "regression"):
		return IntentTest, nil
	case containsAny(t, "change", "modify", "update", "add ", "remove", "refactor", "implement"):
		return IntentChange, nil
	case containsAny(t, "where is", "defined", "definition", "locate", "find symbol"):
		// LOCATE is deliberately last. Evaluation/user prompts often end with
		// generic instructions such as "locate the relevant code" even when the
		// actual task is a security review, debugging, impact, or change request.
		return IntentLocate, nil
	default:
		return IntentUnderstand, nil
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
