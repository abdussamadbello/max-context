package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maxcontext/max-context/internal/mcp"
)

// Tool definitions are re-sent on every request, so their size is a per-turn
// tax paid by every session whether or not the tools are used — for a product
// whose pitch is context efficiency, that number is part of the product.
//
// The budget is a ratchet, not a limit to grow into: if a change pushes the
// definitions over, either trim elsewhere or lower the budget deliberately.
const (
	// Measured in cl100k_base tokens; bytes/4 is a close enough proxy to avoid
	// pulling a tokenizer into this package's test dependencies.
	maxSchemaBytes = 4200

	// No single tool should dominate the budget.
	maxToolBytes = 1250
)

func toolSchemas(t *testing.T) []byte {
	t.Helper()
	// RegisterAll only reads the handles when a tool is invoked; schema
	// construction is pure, so nil handles are fine here.
	schemas := RegisterAll(mcp.NewHandler(), nil, nil, "")
	b, err := json.Marshal(schemas)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestToolSchemaBudget(t *testing.T) {
	b := toolSchemas(t)
	if len(b) > maxSchemaBytes {
		t.Errorf("tool definitions are %d bytes (~%d tokens), over the %d-byte budget.\n"+
			"They are re-sent every request; trim a description or lower the budget on purpose.",
			len(b), len(b)/4, maxSchemaBytes)
	}
	t.Logf("tool definitions: %d bytes (~%d tokens)", len(b), len(b)/4)
}

func TestNoSingleToolDominates(t *testing.T) {
	var schemas []map[string]interface{}
	if err := json.Unmarshal(toolSchemas(t), &schemas); err != nil {
		t.Fatal(err)
	}
	for _, s := range schemas {
		one, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if len(one) > maxToolBytes {
			t.Errorf("%v is %d bytes, over the %d-byte per-tool budget", s["name"], len(one), maxToolBytes)
		}
	}
}

// Trimming must not cost the cross-tool steering the A/B runs tuned: the model
// has to know which tool answers which question, and when to stop searching.
func TestSchemasKeepCrossToolSteering(t *testing.T) {
	var schemas []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(toolSchemas(t), &schemas); err != nil {
		t.Fatal(err)
	}
	desc := map[string]string{}
	for _, s := range schemas {
		desc[s.Name] = s.Description
	}

	for _, tc := range []struct {
		tool  string
		wants []string
		why   string
	}{
		{
			tool:  "get_definition",
			wants: []string{"definitive"},
			why:   "the stop-searching signal is what ends the model's search loop",
		},
		{
			tool:  "query_codebase",
			wants: []string{"get_definition", "get_impact", "get_call_chain"},
			why:   "query_codebase must hand off to the tool that answers the question directly",
		},
	} {
		for _, want := range tc.wants {
			if !strings.Contains(desc[tc.tool], want) {
				t.Errorf("%s description lost %q — %s\ngot: %s", tc.tool, want, tc.why, desc[tc.tool])
			}
		}
	}
}

// Every tool must still declare itself a safe read, or clients cannot
// auto-approve them and each call needs a prompt.
func TestAllToolsAnnotatedReadOnly(t *testing.T) {
	var schemas []struct {
		Name        string `json:"name"`
		Annotations *struct {
			ReadOnlyHint    *bool `json:"readOnlyHint"`
			DestructiveHint *bool `json:"destructiveHint"`
		} `json:"annotations"`
	}
	if err := json.Unmarshal(toolSchemas(t), &schemas); err != nil {
		t.Fatal(err)
	}
	if len(schemas) == 0 {
		t.Fatal("no tools registered")
	}
	for _, s := range schemas {
		if s.Annotations == nil || s.Annotations.ReadOnlyHint == nil || !*s.Annotations.ReadOnlyHint {
			t.Errorf("%s is not annotated readOnlyHint=true", s.Name)
		}
		if s.Annotations != nil && (s.Annotations.DestructiveHint == nil || *s.Annotations.DestructiveHint) {
			t.Errorf("%s is not annotated destructiveHint=false", s.Name)
		}
	}
}

// Each tool needs a description; an empty one gives the model nothing to route on.
func TestEveryToolIsDescribed(t *testing.T) {
	var schemas []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(toolSchemas(t), &schemas); err != nil {
		t.Fatal(err)
	}
	for _, s := range schemas {
		if strings.TrimSpace(s.Description) == "" {
			t.Errorf("%s has no description", s.Name)
		}
		if s.Name == "" {
			t.Errorf("a tool has no name: %+v", s)
		}
	}
}
