package contextcompiler

import "testing"

func TestResolveIntentPrefersTaskSemanticsOverLocateBoilerplate(t *testing.T) {
	tests := []struct {
		task string
		want string
	}{
		{"Perform a security audit, then locate the relevant code.", IntentReview},
		{"Audit high-impact security vulnerabilities and locate the relevant code.", IntentReview},
		{"Debug the panic and locate the relevant code.", IntentDebug},
		{"Explain the blast radius and locate the relevant code.", IntentImpact},
		{"Add refresh-token rotation; locate the relevant code.", IntentChange},
		{"Where is RefreshToken defined?", IntentLocate},
	}
	for _, tt := range tests {
		got, err := resolveIntent(tt.task, IntentAuto)
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Errorf("resolveIntent(%q) = %q, want %q", tt.task, got, tt.want)
		}
	}
}
