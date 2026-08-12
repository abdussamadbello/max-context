package setup

import (
	"fmt"
	"path/filepath"
)

// CommandStyle is how a harness wants the operational commands (reindex, index,
// status) written.
type CommandStyle int

const (
	CommandsNone        CommandStyle = iota // harness has no command concept
	CommandsFrontmatter                     // <dir>/<name>.md with name/description frontmatter
	CommandsVSCodeStyle                     // <dir>/<name>.prompt.md
	CommandsWorkflow                        // <dir>/<name>.md, workflow frontmatter
	CommandsInGuidance                      // documented inside the guidance file itself
)

// Harness is one agent harness max-context knows how to configure.
//
// Adding support for a harness should be a table entry here, not a new file:
// the differences between them are almost entirely *where* files go, not *what*
// goes in them. Anything genuinely unique lives in Extra.
type Harness struct {
	Name string // the `max-context setup <name>` target

	// MCPConfig is where this harness reads its MCP server map, relative to the
	// project root. Empty means the harness has no MCP config to merge — some
	// register servers globally, or only read guidance files.
	MCPConfig string

	// ServersKey is the JSON key holding the server map. Defaults to
	// "mcpServers"; harnesses that use a different key (VS Code's "servers",
	// for instance) set it explicitly. Getting this wrong means setup writes a
	// config the harness silently ignores, so it is stated per harness rather
	// than assumed.
	ServersKey string

	// GuidancePath is the skill / rules file telling the agent to use the tools.
	GuidancePath string
	Guidance     string

	// Commands controls the operational command files.
	Commands    CommandStyle
	CommandsDir string

	// AgentsLine is appended to AGENTS.md inside the sentinel markers.
	AgentsLine string

	// Extra handles anything the fields above cannot express (VS Code's hook
	// scripts, for example). Optional.
	Extra func(root string, r *Report) error
}

const defaultGuidance = "# Max Context\nUse query_codebase and get_architecture.\n"

const defaultAgentsLine = "Use max-context: query_codebase, get_architecture."

// harnesses is the registry. Order matters only for `setup all` output.
var harnesses = []Harness{
	{
		Name:         "claude-code",
		MCPConfig:    ".mcp.json",
		GuidancePath: filepath.Join(".claude", "skills", "max-context", "SKILL.md"),
		Guidance:     defaultGuidance,
		Commands:     CommandsFrontmatter,
		CommandsDir:  filepath.Join(".claude", "commands"),
		AgentsLine:   defaultAgentsLine,
	},
	{
		Name:         "vscode",
		MCPConfig:    filepath.Join(".vscode", "mcp.json"),
		GuidancePath: filepath.Join(".github", "skills", "max-context", "SKILL.md"),
		Guidance:     defaultGuidance,
		Commands:     CommandsVSCodeStyle,
		CommandsDir:  filepath.Join(".github", "prompts"),
		AgentsLine:   defaultAgentsLine,
		Extra:        writeVSCodeHooks,
	},
	{
		Name:         "codex",
		GuidancePath: filepath.Join(".codex", "skills", "max-context", "SKILL.md"),
		Guidance:     defaultGuidance,
		Commands:     CommandsInGuidance,
		AgentsLine:   defaultAgentsLine,
	},
	{
		Name:         "antigravity",
		GuidancePath: filepath.Join(".agent", "skills", "max-context", "SKILL.md"),
		Guidance:     defaultGuidance,
		Commands:     CommandsInGuidance,
		AgentsLine:   defaultAgentsLine,
	},
	{
		Name:         "cursor",
		MCPConfig:    filepath.Join(".cursor", "mcp.json"),
		GuidancePath: filepath.Join(".cursor", "rules", "max-context.md"),
		Guidance:     defaultGuidance,
		Commands:     CommandsFrontmatter,
		CommandsDir:  filepath.Join(".cursor", "commands"),
		AgentsLine:   defaultAgentsLine,
	},
	{
		Name:         "windsurf",
		GuidancePath: filepath.Join(".windsurf", "rules", "max-context.md"),
		Guidance:     "# Max Context\n\nPrefer query_codebase over grep. Use get_architecture for project overview.\n",
		Commands:     CommandsWorkflow,
		CommandsDir:  filepath.Join(".windsurf", "workflows"),
		AgentsLine:   "Use max-context MCP: query_codebase, get_architecture.",
	},
}

// serversKey returns the JSON key this harness stores its server map under.
func (h Harness) serversKey() string {
	if h.ServersKey != "" {
		return h.ServersKey
	}
	return "mcpServers"
}

// apply configures one harness. Every branch is driven by the table above, so a
// new harness needs no new code path.
func (h Harness) apply(root string, r *Report) error {
	if h.MCPConfig != "" {
		if err := mergeMCPConfig(filepath.Join(root, h.MCPConfig), h.serversKey(), r); err != nil {
			return err
		}
	}

	if h.GuidancePath != "" {
		content := h.Guidance
		if content == "" {
			content = defaultGuidance
		}
		if h.Commands == CommandsInGuidance {
			content += renderSkillCommandsSection(Commands)
		}
		if err := writeFileIfAbsent(filepath.Join(root, h.GuidancePath), content, 0644, r); err != nil {
			return err
		}
	}

	if err := h.writeCommands(root, r); err != nil {
		return err
	}

	if h.Extra != nil {
		if err := h.Extra(root, r); err != nil {
			return err
		}
	}

	line := h.AgentsLine
	if line == "" {
		line = defaultAgentsLine
	}
	return appendWithMarkers(filepath.Join(root, "AGENTS.md"), line, r)
}

func (h Harness) writeCommands(root string, r *Report) error {
	if h.Commands == CommandsNone || h.Commands == CommandsInGuidance {
		return nil
	}
	if h.CommandsDir == "" {
		return fmt.Errorf("harness %q sets a command style but no CommandsDir", h.Name)
	}
	dir := filepath.Join(root, h.CommandsDir)
	for _, c := range Commands {
		var name, body string
		switch h.Commands {
		case CommandsFrontmatter:
			name, body = c.Name+".md", renderFrontmatterCommand(c)
		case CommandsVSCodeStyle:
			name, body = c.Name+".prompt.md", renderVSCodePrompt(c)
		case CommandsWorkflow:
			name, body = c.Name+".md", renderWindsurfWorkflow(c)
		default:
			return fmt.Errorf("harness %q has unknown command style %d", h.Name, h.Commands)
		}
		if err := writeFileIfAbsent(filepath.Join(dir, name), body, 0644, r); err != nil {
			return err
		}
	}
	return nil
}

// lookupHarness finds a harness by its setup target name.
func lookupHarness(name string) (Harness, bool) {
	for _, h := range harnesses {
		if h.Name == name {
			return h, true
		}
	}
	return Harness{}, false
}

// HarnessNames lists every configurable target, for CLI help and errors.
func HarnessNames() []string {
	out := make([]string, 0, len(harnesses))
	for _, h := range harnesses {
		out = append(out, h.Name)
	}
	return out
}
