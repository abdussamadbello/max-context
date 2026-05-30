package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Command is one operational command exposed across IDEs.
type Command struct {
	Name        string // file stem, e.g. "reindex"
	Description string // one-line description
	Shell       string // shell command to run
	Body        string // extra markdown guidance
}

var reindexCmd = Command{
	Name:        "reindex",
	Description: "Rebuild the max-context index.",
	Shell:       "max-context --reindex",
	Body: "Run a full reindex so query_codebase and get_architecture use up-to-date data.\n\n" +
		"If the max-context MCP server is running, you can instead trigger a background " +
		"reindex by writing the queue file:\n\n```bash\ntouch .max-context/.reindex-queue\n```",
}

var indexCmd = Command{
	Name:        "index",
	Description: "Build the max-context index for this project.",
	Shell:       "max-context --index",
	Body: "Indexes the current working directory recursively. Run it from the project " +
		"root — never from your home folder or `/`. `cd` into the project first.",
}

var statusCmd = Command{
	Name:        "status",
	Description: "Show max-context index health, file and symbol counts.",
	Shell:       "max-context --status",
	Body:        "Reports whether the index is healthy and how many files and symbols are indexed.",
}

// Commands is the canonical catalog rendered into every IDE.
var Commands = []Command{reindexCmd, indexCmd, statusCmd}

// renderSkillCommandsSection renders the catalog as a "## Commands" markdown
// block for editors without a native command mechanism (Codex, Antigravity).
func renderSkillCommandsSection(cmds []Command) string {
	var b strings.Builder
	b.WriteString("\n## Commands\n\n")
	for _, c := range cmds {
		fmt.Fprintf(&b, "### %s\n\n%s\n\n%s\n\n```bash\n%s\n```\n\n", c.Name, c.Description, c.Body, c.Shell)
	}
	return b.String()
}

// renderFrontmatterCommand renders a command as markdown with YAML frontmatter
// (name + description). Used by Claude Code and Cursor, which share this format.
func renderFrontmatterCommand(c Command) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", c.Name)
	fmt.Fprintf(&b, "description: %s\n", c.Description)
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", c.Description)
	b.WriteString(c.Body)
	b.WriteString("\n\nRun in the project root:\n\n```bash\n")
	b.WriteString(c.Shell)
	b.WriteString("\n```\n")
	return b.String()
}

// writeClaudeCommands renders each command into .claude/commands/<name>.md.
func writeClaudeCommands(root string) error {
	dir := filepath.Join(root, ".claude", "commands")
	for _, c := range Commands {
		path := filepath.Join(dir, c.Name+".md")
		if err := writeIfNotExists(path, renderFrontmatterCommand(c)); err != nil {
			return err
		}
	}
	return nil
}

// writeCursorCommands renders each command into .cursor/commands/<name>.md.
func writeCursorCommands(root string) error {
	dir := filepath.Join(root, ".cursor", "commands")
	for _, c := range Commands {
		path := filepath.Join(dir, c.Name+".md")
		if err := writeIfNotExists(path, renderFrontmatterCommand(c)); err != nil {
			return err
		}
	}
	return nil
}

// renderVSCodePrompt renders a command as a VS Code prompt file (.prompt.md).
func renderVSCodePrompt(c Command) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("agent: agent\n")
	fmt.Fprintf(&b, "description: %s\n", c.Description)
	b.WriteString("---\n\n")
	b.WriteString(c.Body)
	b.WriteString("\n\nRun in the project root:\n\n```bash\n")
	b.WriteString(c.Shell)
	b.WriteString("\n```\n")
	return b.String()
}

// writeVSCodePrompts renders each command into .github/prompts/<name>.prompt.md.
func writeVSCodePrompts(root string) error {
	dir := filepath.Join(root, ".github", "prompts")
	for _, c := range Commands {
		path := filepath.Join(dir, c.Name+".prompt.md")
		if err := writeIfNotExists(path, renderVSCodePrompt(c)); err != nil {
			return err
		}
	}
	return nil
}

// renderWindsurfWorkflow renders a command as a Windsurf workflow file.
func renderWindsurfWorkflow(c Command) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "description: %s\n", c.Description)
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", c.Description)
	b.WriteString(c.Body)
	b.WriteString("\n\n```bash\n")
	b.WriteString(c.Shell)
	b.WriteString("\n```\n")
	return b.String()
}

// writeWindsurfCommands renders each command into .windsurf/workflows/<name>.md.
func writeWindsurfCommands(root string) error {
	dir := filepath.Join(root, ".windsurf", "workflows")
	for _, c := range Commands {
		path := filepath.Join(dir, c.Name+".md")
		if err := writeIfNotExists(path, renderWindsurfWorkflow(c)); err != nil {
			return err
		}
	}
	return nil
}

// writeIfNotExists writes content to path only if it does not already exist,
// creating parent directories. Mirrors the package's idempotent convention.
func writeIfNotExists(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // exists; never overwrite user edits
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0644)
}
