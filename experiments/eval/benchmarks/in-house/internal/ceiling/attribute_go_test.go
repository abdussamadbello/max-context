package ceiling

import "testing"

// The attributor was Python-only, so every hit in a Go file resolved to "" and
// the grep arm scored zero on a Go probe no matter what it had found. That is
// the strawman this harness is built not to be, and it is invisible in the
// output: a harness gap and a genuine retrieval failure both print recall 0.
func TestEnclosingFuncAttributesGo(t *testing.T) {
	src := []string{
		"package dispatch",          // 1
		"",                          // 2
		"type Notifier interface {", // 3
		"\tSend(msg string) error",  // 4
		"}",                         // 5
		"",                          // 6
		"func (e *EmailNotifier) Send(msg string) error {", // 7
		"\treturn nil", // 8
		"}",            // 9
		"",             // 10
		"func DeliverAlert(n Notifier, msg string) error {", // 11
		"\treturn n.Send(msg)",                              // 12
		"}",                                                 // 13
		"",                                                  // 14
		"func BroadcastAll(ns []Notifier, msg string) error {", // 15
		"\tfor _, n := range ns {",                             // 16
		"\t\tif err := n.Send(msg); err != nil {",              // 17
		"\t\t\treturn err",                                     // 18
		"\t\t}",                                                // 19
		"\t}",                                                  // 20
		"\treturn nil",                                         // 21
		"}",                                                    // 22
	}
	for _, tc := range []struct {
		line int
		want string
		why  string
	}{
		{12, "DeliverAlert", "a call in a top-level function attributes to it"},
		{17, "BroadcastAll", "a call nested in a loop still attributes to the function"},
		{8, "Send", "a method body attributes to the method, not its receiver type"},
		{11, "", "a func line belongs to file scope, not to itself"},
		{4, "", "an interface method declaration is not inside a function"},
		{1, "", "file scope has no calling function"},
	} {
		if got := enclosingFunc("pipeline.go", src, tc.line); got != tc.want {
			t.Errorf("line %d: got %q, want %q (%s)", tc.line, got, tc.want, tc.why)
		}
	}
}

// An extension with no pattern must be reported, never scored as a miss.
func TestDefPatternForKnownLanguages(t *testing.T) {
	for _, tc := range []struct {
		path  string
		known bool
	}{
		{"a.py", true},
		{"a.go", true},
		{"a.ts", true},
		{"a.tsx", true},
		{"a.js", true},
		{"a.rs", false},
		{"Makefile", false},
	} {
		if _, ok := defPatternFor(tc.path); ok != tc.known {
			t.Errorf("defPatternFor(%q) known = %v, want %v", tc.path, ok, tc.known)
		}
	}
}

// Receivers are the trap: `func (e *EmailNotifier) Send(...)` must capture Send.
func TestGoFuncReCapturesMethodNameNotReceiver(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{"func Plain(a int) error {", "Plain"},
		{"func (e *EmailNotifier) Send(msg string) error {", "Send"},
		{"func (e EmailNotifier) Send(msg string) error {", "Send"},
		{"func (s *SMSNotifier) Send(msg string) error {", "Send"},
	} {
		m := goFuncRe.FindStringSubmatch(tc.line)
		if m == nil {
			t.Errorf("%q did not match goFuncRe", tc.line)
			continue
		}
		if m[2] != tc.want {
			t.Errorf("%q captured %q, want %q", tc.line, m[2], tc.want)
		}
	}
}

// `for (const n of ns) {` matches "identifier, parens, brace" as surely as a
// method signature does, so a call inside a loop attributed to `for` — grep
// found the line and the harness discarded the answer. That scored the TS probe
// 1/2 for grep when the real figure is 2/2, which is a false claim about the
// baseline, not a finding.
func TestEnclosingFuncIgnoresControlFlow(t *testing.T) {
	src := []string{
		"export function broadcastAll(ns: Notifier[], msg: string): void {", // 1
		"  for (const n of ns) {", // 2
		"    n.send(msg);",        // 3
		"  }",                     // 4
		"}",                       // 5
		"",                        // 6
		"export function retry(n: Notifier): void {", // 7
		"  while (true) {",                           // 8
		"    if (ready()) {",                         // 9
		"      n.send('x');",                         // 10
		"    }",                                      // 11
		"  }",                                        // 12
		"}",                                          // 13
	}
	for _, tc := range []struct {
		line int
		want string
		why  string
	}{
		{3, "broadcastAll", "a call inside a for loop belongs to the enclosing function"},
		{10, "retry", "nested while/if must not capture the call either"},
	} {
		if got := enclosingFunc("pipeline.ts", src, tc.line); got != tc.want {
			t.Errorf("line %d: got %q, want %q (%s)", tc.line, got, tc.want, tc.why)
		}
	}
}

// A TypeScript interface member declares without defining; counting it as a
// definition would attribute the interface's own line to a phantom function.
func TestTsFuncReRequiresABody(t *testing.T) {
	for _, tc := range []struct {
		line  string
		match bool
	}{
		{"export function deliverAlert(n: Notifier): void {", true},
		{"  send(msg: string): void {", true},
		{"  async send(msg: string): Promise<void> {", true},
		{"  send(msg: string): void;", false}, // interface member: declaration only
		{"  private helper(): void {", true},
	} {
		got := tsFuncRe.MatchString(tc.line)
		if got != tc.match {
			t.Errorf("tsFuncRe.MatchString(%q) = %v, want %v", tc.line, got, tc.match)
		}
	}
}
