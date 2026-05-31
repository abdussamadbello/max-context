package indexer

import (
	"context"
	"testing"
)

// TestModuleConstants verifies module/package-level constants are captured as
// searchable symbols across Python, TypeScript, and Go — and that locals /
// class attributes are NOT misclassified as module constants.
func TestModuleConstants(t *testing.T) {
	cases := []struct {
		name      string
		file      string
		src       string
		wantConst []string // names that MUST be captured
		notConst  []string // names that must NOT be captured (locals/attrs)
	}{
		{
			name: "python",
			file: "cfg.py",
			src: "DEFAULT_TIMEOUT_CONFIG = Timeout(timeout=5.0)\n" +
				"MAX = 10\n" +
				"class C:\n    attr = 1\n    def m(self):\n        local = 2\n",
			wantConst: []string{"DEFAULT_TIMEOUT_CONFIG", "MAX"},
			notConst:  []string{"local"}, // class attr `attr` is nested; local is in a func
		},
		{
			// Module-level try/except and if/else guards (optional-import &
			// version-gate fallbacks) must be captured; same-named assignments
			// inside a function must NOT (they are locals).
			name: "python-guarded",
			file: "guard.py",
			src: "try:\n    import fast as _f\n    JSON_BACKEND = 'fast'\n" +
				"except ImportError:\n    JSON_BACKEND = 'slow'\n" +
				"import sys\nif sys.version_info >= (3, 8):\n    FEATURE = True\nelse:\n    FEATURE = False\n" +
				"def f():\n    try:\n        LOCAL_IN_TRY = 1\n    except Exception:\n        LOCAL_IN_TRY = 2\n    return LOCAL_IN_TRY\n",
			wantConst: []string{"JSON_BACKEND", "FEATURE"},
			notConst:  []string{"LOCAL_IN_TRY"}, // inside a function's try -> local, not a module const
		},
		{
			name: "typescript",
			file: "cfg.ts",
			src: "export const MAX_RETRIES = 3;\n" +
				"const defaultClient = makeClient();\n" +
				"function f() { const inner = 1; return inner; }\n",
			wantConst: []string{"MAX_RETRIES", "defaultClient"},
			notConst:  []string{"inner"},
		},
		{
			name: "go",
			file: "cfg.go",
			src: "package p\n" +
				"const MaxN = 10\n" +
				"var DefaultCfg = NewConfig()\n" +
				"func f() { const inner = 1; _ = inner }\n",
			wantConst: []string{"MaxN", "DefaultCfg"},
			notConst:  []string{"inner"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ParseFile(context.Background(), tc.file, []byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			for _, c := range res.Consts {
				got[c.Name] = true
			}
			for _, w := range tc.wantConst {
				if !got[w] {
					t.Errorf("%s: expected constant %q to be captured; got %v", tc.name, w, keys(got))
				}
			}
			for _, n := range tc.notConst {
				if got[n] {
					t.Errorf("%s: %q must NOT be captured as a module constant", tc.name, n)
				}
			}
		})
	}
}

func keys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}
