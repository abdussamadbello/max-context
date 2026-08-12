package main

import (
	"flag"
	"reflect"
	"testing"
)

// Go's flag package stops parsing at the first non-flag argument, so every CLI
// example in the README — all of which put the positional first — silently ran
// with default values. `calls Migrate -direction callers -depth 3` came back
// with depth 2 and both directions, with no error to tell the user.
func TestParseFlagsAnywhere(t *testing.T) {
	newSet := func() (*flag.FlagSet, *string, *int, *bool) {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.SetOutput(discard{})
		direction := fs.String("direction", "both", "")
		depth := fs.Int("depth", 2, "")
		includeTests := fs.Bool("include-tests", true, "")
		return fs, direction, depth, includeTests
	}

	for _, tc := range []struct {
		name           string
		args           []string
		wantDirection  string
		wantDepth      int
		wantTests      bool
		wantPositional []string
	}{
		{
			name:           "flags after positional (the documented order)",
			args:           []string{"Migrate", "-direction", "callers", "-depth", "3"},
			wantDirection:  "callers",
			wantDepth:      3,
			wantTests:      true,
			wantPositional: []string{"Migrate"},
		},
		{
			name:           "flags before positional",
			args:           []string{"-direction", "callers", "-depth", "3", "Migrate"},
			wantDirection:  "callers",
			wantDepth:      3,
			wantTests:      true,
			wantPositional: []string{"Migrate"},
		},
		{
			name:           "flags on both sides",
			args:           []string{"-depth", "4", "Migrate", "-direction", "callees"},
			wantDirection:  "callees",
			wantDepth:      4,
			wantTests:      true,
			wantPositional: []string{"Migrate"},
		},
		{
			name:           "double-dash form",
			args:           []string{"Migrate", "--direction", "callers"},
			wantDirection:  "callers",
			wantDepth:      2,
			wantTests:      true,
			wantPositional: []string{"Migrate"},
		},
		{
			name:           "inline value",
			args:           []string{"Migrate", "-direction=callers", "-depth=5"},
			wantDirection:  "callers",
			wantDepth:      5,
			wantTests:      true,
			wantPositional: []string{"Migrate"},
		},
		{
			name:           "bool flag consumes no value",
			args:           []string{"-include-tests=false", "a.go", "b.go"},
			wantDirection:  "both",
			wantDepth:      2,
			wantTests:      false,
			wantPositional: []string{"a.go", "b.go"},
		},
		{
			name:           "bare bool flag before positionals",
			args:           []string{"-include-tests", "a.go", "b.go"},
			wantDirection:  "both",
			wantDepth:      2,
			wantTests:      true,
			wantPositional: []string{"a.go", "b.go"},
		},
		{
			name:           "multiple positionals with interleaved flags",
			args:           []string{"a.go", "-depth", "3", "b.go", "c.go"},
			wantDirection:  "both",
			wantDepth:      3,
			wantTests:      true,
			wantPositional: []string{"a.go", "b.go", "c.go"},
		},
		{
			name:           "double dash ends flag parsing",
			args:           []string{"-depth", "3", "--", "-not-a-flag"},
			wantDirection:  "both",
			wantDepth:      3,
			wantTests:      true,
			wantPositional: []string{"-not-a-flag"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs, direction, depth, includeTests := newSet()
			if err := parseFlagsAnywhere(fs, tc.args); err != nil {
				t.Fatalf("parse %v: %v", tc.args, err)
			}
			if *direction != tc.wantDirection {
				t.Errorf("direction = %q, want %q", *direction, tc.wantDirection)
			}
			if *depth != tc.wantDepth {
				t.Errorf("depth = %d, want %d", *depth, tc.wantDepth)
			}
			if *includeTests != tc.wantTests {
				t.Errorf("include-tests = %v, want %v", *includeTests, tc.wantTests)
			}
			if got := fs.Args(); !reflect.DeepEqual(got, tc.wantPositional) {
				t.Errorf("positional = %v, want %v", got, tc.wantPositional)
			}
		})
	}
}

// An unknown flag must still be an error rather than being swallowed as a
// positional argument.
func TestParseFlagsAnywhereRejectsUnknownFlag(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(discard{})
	fs.Int("depth", 2, "")
	if err := parseFlagsAnywhere(fs, []string{"Migrate", "-nope", "1"}); err == nil {
		t.Error("expected an error for an undefined flag")
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
