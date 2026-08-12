package setup

import (
	"fmt"
	"os"
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

// ConfigFormat is how a harness serialises its MCP config.
type ConfigFormat int

const (
	FormatJSON ConfigFormat = iota
	FormatYAML
	FormatTOML
)

// ServerEntryStyle is the shape of a single server definition. Harnesses do not
// agree on this: most take a command string plus an args array, while opencode
// takes a typed entry whose command is one array of argv.
type ServerEntryStyle int

const (
	// EntryCommandArgs is {"command": "max-context", "args": []}.
	EntryCommandArgs ServerEntryStyle = iota
	// EntryTypedArgv is {"type": "local", "command": ["max-context"], "enabled": true}.
	EntryTypedArgv
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

	// ServersKey is the key holding the server map. Defaults to "mcpServers";
	// harnesses using a different key set it explicitly. Getting this wrong
	// means setup writes a config the harness silently ignores, so it is stated
	// per harness rather than assumed.
	ServersKey string

	// Format is how the config is serialised. Hermes uses YAML; the rest JSON.
	Format ConfigFormat

	// EntryStyle is the shape of one server definition.
	EntryStyle ServerEntryStyle

	// HomeRelative makes MCPConfig relative to the user's home directory rather
	// than the project. Some harnesses only have a global server registry, so
	// configuring them touches a file outside the repo — the report says so.
	HomeRelative bool

	// InstructionsKey, when set, is a config key holding an array of paths to
	// instruction files. Harnesses without an AGENTS.md convention (opencode)
	// discover guidance this way instead.
	InstructionsKey string

	// NoAgentsMD skips the AGENTS.md block for harnesses that do not read it.
	NoAgentsMD bool

	// GuidancePath is the skill / rules file telling the agent to use the tools.
	GuidancePath string
	Guidance     string

	// Commands controls the operational command files.
	Commands    CommandStyle
	CommandsDir string

	// AgentsLine is appended to AGENTS.md inside the sentinel markers.
	AgentsLine string

	// Note is printed after setup when using this harness needs a step
	// max-context cannot take on the user's behalf.
	Note string

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
		// Codex reads TOML, keying servers under [mcp_servers.<name>]. Config is
		// layered: ~/.codex/config.toml globally, .codex/config.toml per project.
		// The project layer is written here so the setting travels with the repo
		// — but Codex only loads it for projects the user has marked trusted, so
		// Note says how, rather than leaving a config that looks applied and is
		// not. https://learn.chatgpt.com/docs/extend/mcp?surface=cli
		Name:         "codex",
		MCPConfig:    filepath.Join(".codex", "config.toml"),
		ServersKey:   "mcp_servers",
		Format:       FormatTOML,
		GuidancePath: filepath.Join(".codex", "skills", "max-context", "SKILL.md"),
		Guidance:     defaultGuidance,
		Commands:     CommandsInGuidance,
		AgentsLine:   defaultAgentsLine,
		Note: "Codex loads .codex/config.toml only for projects you have marked trusted. " +
			"If max-context does not appear in /mcp, add this to ~/.codex/config.toml:\n" +
			"    [projects.\"<absolute path to this project>\"]\n" +
			"    trust_level = \"trusted\"",
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
	{
		// opencode reads opencode.json at the project root, keys servers under
		// "mcp", and wants a typed entry whose command is one argv array. It has
		// no AGENTS.md convention: guidance is discovered through the
		// "instructions" array, so the file is listed there rather than assumed.
		// https://opencode.ai/docs/mcp-servers/ and /docs/config/
		Name:            "opencode",
		MCPConfig:       "opencode.json",
		ServersKey:      "mcp",
		EntryStyle:      EntryTypedArgv,
		InstructionsKey: "instructions",
		NoAgentsMD:      true,
		GuidancePath:    filepath.Join(".opencode", "max-context.md"),
		Guidance:        defaultGuidance,
	},
	{
		// Hermes keeps a single global config in YAML at ~/.hermes/config.yaml
		// and keys servers under "mcp_servers". There is no per-project MCP
		// config, so configuring it writes outside the repo — the report says
		// which file was touched. Skills are the agentskills.io layout under
		// ~/.hermes/skills/, but the project-local copy is what ships with the
		// repo, so guidance stays in-tree.
		// https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp
		Name:         "hermes",
		MCPConfig:    filepath.Join(".hermes", "config.yaml"),
		ServersKey:   "mcp_servers",
		Format:       FormatYAML,
		HomeRelative: true,
		GuidancePath: filepath.Join(".hermes", "skills", "max-context", "SKILL.md"),
		Guidance:     defaultGuidance,
		Commands:     CommandsInGuidance,
		AgentsLine:   defaultAgentsLine,
	},
	{
		// pi has no MCP support at all — a deliberate design choice ("No MCP.
		// Build CLI tools with READMEs (see Skills), or build an extension that
		// adds MCP support."). So pi is served by the skill + one-shot CLI path:
		// a SKILL.md in the Agent Skills layout it already discovers, plus the
		// AGENTS.md block it loads at startup.
		// https://github.com/earendil-works/pi
		Name:         "pi",
		GuidancePath: filepath.Join(".pi", "skills", "max-context", "SKILL.md"),
		Guidance:     piGuidance,
		Commands:     CommandsInGuidance,
		AgentsLine:   "Use max-context via its CLI: `max-context query <text>`, `max-context def <symbol>`, `max-context calls <fn>`, `max-context impact`, `max-context arch`. Prefer these over grep for codebase search.",
	},
}

// piGuidance documents the CLI rather than MCP tools: pi ships no MCP client,
// so the one-shot subcommands are the whole integration surface.
const piGuidance = `# Max Context

Search this codebase through the max-context index instead of grepping. The
index is pre-built and kept current; every command prints JSON to stdout.

| Question | Command |
|---|---|
| Where is X defined? | ` + "`max-context def X`" + ` |
| Find code by keyword | ` + "`max-context query \"some words\" -n 5`" + ` |
| Who calls this? | ` + "`max-context calls Name -direction callers`" + ` |
| What does my change break? | ` + "`max-context impact -from-git HEAD`" + ` |
| Project overview | ` + "`max-context arch`" + ` |

Responses carry ` + "`answer_status`" + ` and ` + "`recommended_next_action`" + `. When
` + "`answer_status`" + ` is ` + "`definitive`" + `, answer from that result without searching again.

If a command reports the index is not ready, run ` + "`max-context --index`" + ` once.
`

// userHomeDir is indirected so tests can point home-relative harnesses at a
// temp directory instead of writing to the developer's real home.
var userHomeDir = os.UserHomeDir

// mcpConfigPath resolves where this harness keeps its MCP config.
func (h Harness) mcpConfigPath(root string) (string, error) {
	if !h.HomeRelative {
		return filepath.Join(root, h.MCPConfig), nil
	}
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory for %s config: %w", h.Name, err)
	}
	return filepath.Join(home, h.MCPConfig), nil
}

// serverEntry builds the server definition in this harness's shape.
func (h Harness) serverEntry() map[string]interface{} {
	if h.EntryStyle == EntryTypedArgv {
		return map[string]interface{}{
			"type":    "local",
			"command": []interface{}{serverName},
			"enabled": true,
		}
	}
	return mcpServerEntry()
}

// serversKey returns the key this harness stores its server map under.
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
		path, err := h.mcpConfigPath(root)
		if err != nil {
			return err
		}
		switch h.Format {
		case FormatYAML:
			err = mergeYAMLConfig(path, h.serversKey(), h.serverEntry(), r)
		case FormatTOML:
			err = mergeTOMLConfig(path, h.serversKey(), h.serverEntry(), r)
		default:
			err = mergeMCPConfigEntry(path, h.serversKey(), h.serverEntry(), r)
		}
		if err != nil {
			return err
		}
		r.note(h.Note)
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

	if h.InstructionsKey != "" && h.GuidancePath != "" {
		path, err := h.mcpConfigPath(root)
		if err != nil {
			return err
		}
		if err := addInstructionsPath(path, h.InstructionsKey, h.GuidancePath, r); err != nil {
			return err
		}
	}

	if h.NoAgentsMD {
		return nil
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
