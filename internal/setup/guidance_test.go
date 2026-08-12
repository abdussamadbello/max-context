package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bug this file exists for: setup wrote a 55-byte stub with no frontmatter
// as every harness's SKILL.md. The Agent Skills layout keys loading on the
// frontmatter `description` — without it the harness has nothing to match a
// request against, so the skill may never load at all. Meanwhile the good
// guidance sat unused in templates/, which no code ever read.
func TestEverySkillFileHasFrontmatter(t *testing.T) {
	for _, h := range harnesses {
		if h.GuidancePath == "" || !h.wantsFrontmatter() {
			continue
		}
		t.Run(h.Name, func(t *testing.T) {
			body := h.guidance()
			if !strings.HasPrefix(body, "---\n") {
				t.Fatalf("%s guidance has no frontmatter:\n%s", h.GuidancePath, firstLines(body, 3))
			}
			name := frontmatterField(body, "name")
			desc := frontmatterField(body, "description")
			if name == "" {
				t.Errorf("frontmatter has no name")
			}
			if desc == "" {
				t.Fatalf("frontmatter has no description — nothing for the harness to trigger on")
			}
			// A description that does not name the situations it applies to
			// cannot be matched against a user's request.
			for _, trigger := range []string{"where is", "who calls", "grep"} {
				if !strings.Contains(strings.ToLower(desc), trigger) {
					t.Errorf("description does not mention %q, so it is unlikely to trigger:\n%s", trigger, desc)
				}
			}
			if len(desc) < 80 {
				t.Errorf("description is only %d chars; too thin to match on:\n%s", len(desc), desc)
			}
		})
	}
}

// Rules and instruction files are plain markdown; frontmatter would render as
// literal text at the top of the document.
func TestNonSkillGuidanceHasNoFrontmatter(t *testing.T) {
	for _, h := range harnesses {
		if h.GuidancePath == "" || h.wantsFrontmatter() {
			continue
		}
		t.Run(h.Name, func(t *testing.T) {
			body := h.guidance()
			if strings.HasPrefix(body, "---") {
				t.Errorf("%s is not a SKILL.md but carries frontmatter:\n%s", h.GuidancePath, firstLines(body, 4))
			}
			if !strings.HasPrefix(body, "# Max Context") {
				t.Errorf("%s does not start with the heading:\n%s", h.GuidancePath, firstLines(body, 3))
			}
		})
	}
}

// The regression that motivated deriving the style: Windsurf and Antigravity
// register no MCP server, yet their guidance told the agent to call
// query_codebase. Guidance must never name tools the harness cannot reach.
func TestGuidanceNeverNamesUnreachableTools(t *testing.T) {
	mcpToolNames := []string{"query_codebase", "get_definition", "get_call_chain", "get_impact", "get_architecture"}
	for _, h := range harnesses {
		if h.GuidancePath == "" {
			continue
		}
		t.Run(h.Name, func(t *testing.T) {
			body := h.guidance()
			if h.MCPConfig != "" {
				for _, tool := range mcpToolNames {
					if !strings.Contains(body, tool) {
						t.Errorf("%s registers a server but its guidance never mentions %s", h.Name, tool)
					}
				}
				return
			}
			// No server registered: the CLI is the only way in.
			for _, tool := range mcpToolNames {
				if strings.Contains(body, tool+"(") || strings.Contains(body, "call `"+tool+"`") {
					t.Errorf("%s registers no MCP server but its guidance tells the agent to call %s", h.Name, tool)
				}
			}
			for _, cmd := range []string{"max-context def", "max-context query", "max-context impact"} {
				if !strings.Contains(body, cmd) {
					t.Errorf("%s has no MCP server, so guidance must document %q:\n%s", h.Name, cmd, body)
				}
			}
		})
	}
}

// The AGENTS.md block must agree with the guidance about how to reach the index.
func TestAgentsLineMatchesGuidanceStyle(t *testing.T) {
	for _, h := range harnesses {
		t.Run(h.Name, func(t *testing.T) {
			line := h.agentsLine()
			if line == "" {
				t.Fatal("empty AGENTS.md line")
			}
			if h.MCPConfig == "" && strings.Contains(line, "query_codebase") {
				t.Errorf("%s registers no server but its AGENTS.md line names MCP tools: %s", h.Name, line)
			}
			if h.MCPConfig != "" && !strings.Contains(line, "get_definition") {
				t.Errorf("%s registers a server but its AGENTS.md line does not name the tools: %s", h.Name, line)
			}
		})
	}
}

// The steering the A/B runs tuned must survive in whatever setup writes — this
// is the text that stops the model looping on keyword search.
func TestGuidanceKeepsTheLoopGuard(t *testing.T) {
	for name, body := range map[string]string{"mcp": guidanceMCP, "cli": guidanceCLI} {
		t.Run(name, func(t *testing.T) {
			for _, want := range []string{
				"answer_status",
				"definitive",
				"third",        // "if you find yourself issuing a third query..."
				"do NOT",       // the explicit prohibition on re-querying
				"blast radius", // the get_impact framing
			} {
				if !strings.Contains(body, want) {
					t.Errorf("guidance lost %q — this is the steering the eval runs tuned", want)
				}
			}
		})
	}
}

// End to end: what lands on disk is what was rendered.
func TestSetupWritesTheRealSkill(t *testing.T) {
	root := t.TempDir()
	if _, err := Run(root, "claude-code"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".claude", "skills", "max-context", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if len(body) < 1000 {
		t.Errorf("skill is only %d bytes — the stub was 55 and taught the agent nothing:\n%s", len(body), body)
	}
	if !strings.HasPrefix(body, "---\n") {
		t.Error("written skill has no frontmatter")
	}
	if frontmatterField(body, "description") == "" {
		t.Error("written skill has no description, so it will not trigger")
	}
}

func TestStripFrontmatter(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"removes a block", "---\nname: x\n---\n\n# Title\nbody\n", "# Title\nbody\n"},
		{"leaves plain markdown", "# Title\nbody\n", "# Title\nbody\n"},
		{"leaves an unterminated block", "---\nname: x\n# Title\n", "---\nname: x\n# Title\n"},
		{"handles no trailing blank line", "---\nname: x\n---\n# Title\n", "# Title\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripFrontmatter(tc.in); got != tc.want {
				t.Errorf("stripFrontmatter(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFrontmatterField(t *testing.T) {
	doc := "---\nname: max-context\ndescription: Use when finding code\n---\n\n# Body\n"
	if got := frontmatterField(doc, "name"); got != "max-context" {
		t.Errorf("name = %q", got)
	}
	if got := frontmatterField(doc, "description"); got != "Use when finding code" {
		t.Errorf("description = %q", got)
	}
	if got := frontmatterField(doc, "missing"); got != "" {
		t.Errorf("missing = %q, want empty", got)
	}
	if got := frontmatterField("# no frontmatter\n", "name"); got != "" {
		t.Errorf("plain markdown returned %q", got)
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// The Windows CI failure this guards: GitHub's Windows runners check out with
// autocrlf=true, so the embedded markdown arrived as CRLF and the "---\n"
// fence never matched. Frontmatter was left in the rules files that must not
// have it, and went undetected in the skills that need it — inverted on both
// sides. .gitattributes pins the checkout to LF; this keeps the parsing correct
// even for content that reaches us unnormalised.
func TestFrontmatterHandlingIsCRLFSafe(t *testing.T) {
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

	doc := "---\nname: max-context\ndescription: Use when finding code\n---\n\n# Max Context\nbody\n"

	if got := frontmatterField(crlf(doc), "description"); got != "Use when finding code" {
		t.Errorf("CRLF frontmatter field = %q, want the value", got)
	}
	stripped := stripFrontmatter(crlf(doc))
	if strings.HasPrefix(stripped, "---") {
		t.Errorf("CRLF frontmatter was not stripped:\n%q", stripped)
	}
	if !strings.HasPrefix(stripped, "# Max Context") {
		t.Errorf("CRLF strip left the wrong content:\n%q", stripped)
	}
}

// The embedded guidance must be LF whatever the checkout did, since every
// downstream check keys on "---\n".
func TestEmbeddedGuidanceIsNormalised(t *testing.T) {
	for name, body := range map[string]string{"mcp": guidanceMCP, "cli": guidanceCLI} {
		if strings.Contains(body, "\r") {
			t.Errorf("%s guidance still contains CR; the fence checks will not match", name)
		}
		if !strings.HasPrefix(body, "---\n") {
			t.Errorf("%s guidance does not open with an LF frontmatter fence", name)
		}
	}
}
