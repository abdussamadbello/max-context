package indexer

import (
	"os"
	"path/filepath"
	"testing"
)

// `ns.member()` resolved in Go and in neither of the others. Go's parser
// recorded a candidate package and the resolver linked on a unique match;
// TypeScript and Python recorded the alias and stopped, so the identical
// pattern resolved in one language only.
func TestNamespaceImportResolvesInEveryDeepLanguage(t *testing.T) {
	for _, tc := range []struct {
		name       string
		files      map[string]string
		method     string
		wantCaller string
	}{
		{
			name:   "typescript",
			method: "postTransaction", wantCaller: "useNs",
			files: map[string]string{
				"ledger.ts": "export function postTransaction(n: number): void {}\n",
				"a.ts":      "import * as ledger from \"./ledger\";\nexport function useNs(): void { ledger.postTransaction(1); }\n",
			},
		},
		{
			name:   "python",
			method: "post_transaction", wantCaller: "use_ns",
			files: map[string]string{
				"ledger.py": "def post_transaction(n):\n    pass\n",
				"a.py":      "import ledger\n\n\ndef use_ns():\n    ledger.post_transaction(1)\n",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for name, body := range tc.files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if callers := callersOfMethod(t, root, tc.method); !callers[tc.wantCaller] {
				t.Errorf("%s: %s calls %s through a namespace import but does not resolve; got %v",
					tc.name, tc.wantCaller, tc.method, keysOf(callers))
			}
		})
	}
}

// The renamed-import binding is the mechanism behind this project's one
// published quality win (FINDINGS.md v4: grep 0/5, max-context 5/5). Sharing
// the code that records it must not cost it, in either language that has it.
func TestRenamedImportStillResolves(t *testing.T) {
	for _, tc := range []struct {
		name       string
		files      map[string]string
		method     string
		wantCaller string
	}{
		{
			name:   "typescript",
			method: "postTransaction", wantCaller: "chargeSubscription",
			files: map[string]string{
				"ledger.ts":  "export function postTransaction(n: number): void {}\n",
				"billing.ts": "import { postTransaction as applyEntry } from \"./ledger\";\nexport function chargeSubscription(): void { applyEntry(1); }\n",
			},
		},
		{
			name:   "python",
			method: "post_transaction", wantCaller: "charge_subscription",
			files: map[string]string{
				"ledger.py":  "def post_transaction(n):\n    pass\n",
				"billing.py": "from ledger import post_transaction as apply_entry\n\n\ndef charge_subscription():\n    apply_entry(1)\n",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for name, body := range tc.files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if callers := callersOfMethod(t, root, tc.method); !callers[tc.wantCaller] {
				t.Errorf("%s: the call site names only the alias, so %s must reach %s through the import; got %v",
					tc.name, tc.wantCaller, tc.method, keysOf(callers))
			}
		})
	}
}

func TestRecordImportedSymbol(t *testing.T) {
	m := map[string]importedSym{}
	recordImportedSymbol(m, "postTransaction", "", "")
	recordImportedSymbol(m, "settle", "applyEntry", "pkg")
	// An origin the parser could not read must not be stored under "", which
	// would match every bare call whose name also came back empty.
	recordImportedSymbol(m, "", "ghost", "")

	if got := m["postTransaction"]; got.origin != "postTransaction" {
		t.Errorf("unaliased import: origin = %q, want postTransaction", got.origin)
	}
	if got := m["applyEntry"]; got.origin != "settle" || got.root != "pkg" {
		t.Errorf("aliased import: got (%q,%q), want (settle,pkg)", got.origin, got.root)
	}
	if _, present := m[""]; present {
		t.Error("an empty origin was stored under the empty local name")
	}
	if len(m) != 2 {
		t.Errorf("recorded %d bindings, want 2", len(m))
	}
}

func TestRelativeModuleFile(t *testing.T) {
	for _, tc := range []struct {
		name, from, spec, ext, want, why string
	}{
		{
			name: "sibling module", from: "src/a.ts", spec: "./ledger", ext: ".ts", want: "src/ledger.ts",
			why: "the specifier is relative to the importing file's directory",
		},
		{
			name: "parent module", from: "src/deep/a.ts", spec: "../ledger", ext: ".ts", want: "src/ledger.ts",
		},
		{
			name: "explicit extension is kept", from: "src/a.ts", spec: "./ledger.js", ext: ".ts", want: "src/ledger.js",
			why: "an ESM specifier already names the file",
		},
		{
			name: "bare specifier is not a repo module", from: "src/a.ts", spec: "lodash", ext: ".ts", want: "",
			why: "it names a package outside the repository; a key here would match nothing",
		},
		{
			name: "scoped package is not a repo module", from: "src/a.ts", spec: "@scope/pkg", ext: ".ts", want: "",
		},
		{
			name: "empty specifier", from: "src/a.ts", spec: "", ext: ".ts", want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := relativeModuleFile(tc.from, tc.spec, tc.ext); got != tc.want {
				t.Errorf("relativeModuleFile(%q,%q,%q) = %q, want %q — %s",
					tc.from, tc.spec, tc.ext, got, tc.want, tc.why)
			}
		})
	}
}

func TestPythonModuleFile(t *testing.T) {
	for _, tc := range []struct{ name, from, module, want string }{
		{name: "top-level module", from: "app/a.py", module: "ledger", want: "ledger.py"},
		{name: "dotted module", from: "app/a.py", module: "pkg.ledger", want: "pkg/ledger.py"},
		{name: "relative module", from: "app/a.py", module: ".ledger", want: "app/ledger.py"},
		{name: "parent-relative module", from: "app/deep/a.py", module: "..ledger", want: "app/ledger.py"},
		{name: "bare dots name no module", from: "app/a.py", module: "...", want: ""},
		{name: "empty", from: "app/a.py", module: "", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pythonModuleFile(tc.from, tc.module); got != tc.want {
				t.Errorf("pythonModuleFile(%q,%q) = %q, want %q", tc.from, tc.module, got, tc.want)
			}
		})
	}
}
