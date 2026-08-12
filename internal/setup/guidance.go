package setup

import (
	_ "embed"
	"strings"
)

// The guidance an agent reads is the whole product surface for harnesses with
// no MCP client, and the difference between a used and an ignored tool
// everywhere else. It lives in reviewable markdown files rather than string
// literals, and is embedded so it actually ships — the previous templates/
// directory was documented as "emitted by max-context setup" and was never
// read by any code, while setup wrote a two-line stub instead.
var (
	//go:embed guidance/skill-mcp.md
	rawGuidanceMCP string

	//go:embed guidance/skill-cli.md
	rawGuidanceCLI string
)

// Normalised at init: a Windows checkout with autocrlf=true rewrites these
// files to CRLF, and every "---\n" fence check below would stop matching —
// frontmatter would survive in rules files that must not have it, and go
// undetected in the skills that need it. .gitattributes pins the checkout to
// LF; this makes the code correct even where it does not.
var (
	guidanceMCP = normaliseNewlines(rawGuidanceMCP)
	guidanceCLI = normaliseNewlines(rawGuidanceCLI)
)

func normaliseNewlines(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

// GuidanceStyle selects how the agent is told to reach the index.
type GuidanceStyle int

const (
	// GuidanceForMCP documents the MCP tools. Only correct when the harness
	// actually has the server registered.
	GuidanceForMCP GuidanceStyle = iota
	// GuidanceForCLI documents the one-shot subcommands, for harnesses with no
	// MCP client (pi) or none configured yet.
	GuidanceForCLI
)

// guidanceStyle picks the style from what the harness actually configures.
//
// Deriving it rather than storing it per harness closes a whole failure mode:
// guidance can never tell an agent to call query_codebase in a harness where no
// server was registered. Windsurf and Antigravity did exactly that.
func (h Harness) guidanceStyle() GuidanceStyle {
	if h.MCPConfig != "" {
		return GuidanceForMCP
	}
	return GuidanceForCLI
}

// wantsFrontmatter reports whether this harness's guidance file is a skill in
// the Agent Skills layout, which is keyed on YAML frontmatter: the description
// is what the harness matches a request against to decide whether to load the
// body. A SKILL.md without it may never load at all. Rules and instruction
// files are plain markdown and would render the frontmatter as text.
func (h Harness) wantsFrontmatter() bool {
	return strings.HasSuffix(h.GuidancePath, "SKILL.md")
}

// guidance renders the guidance file for this harness.
func (h Harness) guidance() string {
	body := guidanceMCP
	if h.guidanceStyle() == GuidanceForCLI {
		body = guidanceCLI
	}
	if !h.wantsFrontmatter() {
		body = stripFrontmatter(body)
	}
	if h.Commands == CommandsInGuidance {
		body += renderSkillCommandsSection(Commands)
	}
	return body
}

// agentsLine renders the AGENTS.md block, matching the guidance style so the
// two never disagree about how to reach the index.
func (h Harness) agentsLine() string {
	if h.AgentsLine != "" {
		return h.AgentsLine
	}
	if h.guidanceStyle() == GuidanceForCLI {
		return "Search this codebase with max-context instead of grep: " +
			"`max-context def <symbol>` for definitions, `max-context query \"<words>\"` for keywords, " +
			"`max-context calls <fn>` for callers, `max-context impact` for the blast radius of a change."
	}
	return "Search this codebase with the max-context MCP tools instead of grep: " +
		"get_definition for definitions, query_codebase for keywords, " +
		"get_call_chain and get_impact for what uses or breaks a symbol."
}

// stripFrontmatter removes a leading YAML frontmatter block, if present.
// Tolerates CRLF so a file that reached us unnormalised still parses.
func stripFrontmatter(s string) string {
	s = normaliseNewlines(s)
	const fence = "---\n"
	if !strings.HasPrefix(s, fence) {
		return s
	}
	rest := s[len(fence):]
	end := strings.Index(rest, "\n"+fence)
	if end < 0 {
		return s // unterminated; leave it rather than truncate the document
	}
	return strings.TrimLeft(rest[end+len("\n")+len(fence):], "\n")
}

// frontmatterField reads one top-level scalar from a guidance file's
// frontmatter. Used by tests to assert the fields a harness needs to load it.
func frontmatterField(s, key string) string {
	s = normaliseNewlines(s)
	const fence = "---\n"
	if !strings.HasPrefix(s, fence) {
		return ""
	}
	rest := s[len(fence):]
	end := strings.Index(rest, "\n"+fence)
	if end < 0 {
		return ""
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		name, value, found := strings.Cut(line, ":")
		if found && strings.TrimSpace(name) == key {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
