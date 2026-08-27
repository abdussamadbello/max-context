package arms

import (
	"context"
	"strings"
	"testing"
)

func TestContextArmSuppliesPackageOnce(t *testing.T) {
	t.Parallel()

	calls := 0
	arm := &ContextArm{run: func(context.Context) ([]byte, error) {
		calls++
		return []byte(`{"token_budget":4000,"tokens_used":123}`), nil
	}}
	tools := arm.Tools()
	if len(tools) != 1 || tools[0].Name != contextToolName {
		t.Fatalf("tools = %#v", tools)
	}

	got, isErr := arm.Execute(context.Background(), contextToolName, nil)
	if isErr || !strings.Contains(got, `"tokens_used":123`) {
		t.Fatalf("first call = (%q, %v)", got, isErr)
	}
	got, isErr = arm.Execute(context.Background(), contextToolName, nil)
	if !isErr || !strings.Contains(got, "already supplied") {
		t.Fatalf("second call = (%q, %v)", got, isErr)
	}
	if calls != 1 {
		t.Fatalf("compiler calls = %d, want 1", calls)
	}
}

func TestContextArmRejectsUnknownTool(t *testing.T) {
	t.Parallel()

	arm := &ContextArm{run: func(context.Context) ([]byte, error) {
		t.Fatal("compiler should not run")
		return nil, nil
	}}
	if _, isErr := arm.Execute(context.Background(), "other", nil); !isErr {
		t.Fatal("unknown tool should fail")
	}
}
