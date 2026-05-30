# Cross-IDE Operational Commands Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Generate three operational commands (`reindex`, `index`, `status`) during `max-context setup <cli>`, rendered into each IDE's native command format where one exists and folded into the skill doc where it doesn't.

**Architecture:** A single Go catalog (`internal/setup/commands.go`) defines the three commands once. Per-IDE render functions translate each `Command` into that IDE's file format (Claude Code / Cursor / VS Code prompt files / Windsurf workflows as real commands; Codex / Antigravity as a SKILL.md section). Each existing per-CLI setup writer gains a loop that renders + writes command files using the existing `ensureDir` + write-if-not-exists idempotent pattern.

**Tech Stack:** Go 1.22+ (CGO enabled), standard library only (`os`, `path/filepath`, `strings`, `fmt`). Tests via `go test` with `t.TempDir()`. Build/verify via `make build`, `make test`, `make lint`.

---

## File Structure

```
internal/setup/
  commands.go        NEW — Command struct, Commands catalog, 5 render funcs, writeCommandFiles helpers
  commands_test.go   NEW — catalog render tests + per-IDE file assertions + idempotency
  claude_code.go     MOD — call writeClaudeCommands(root) after existing writes
  cursor.go          MOD — call writeCursorCommands(root)
  windsurf.go        MOD — replace inline reindex workflow with writeWindsurfCommands(root)
  vscode.go          MOD — call writeVSCodePrompts(root)
  codex.go           MOD — append commands section to SKILL.md content
  antigravity.go     MOD — append commands section to SKILL.md content
```

**Catalog data contract** (defined in Task 1, used by all later tasks — names must match exactly):

```go
type Command struct {
	Name        string // file stem, e.g. "reindex"
	Description string // one-line frontmatter description
	Shell       string // the shell command to run, e.g. "max-context --reindex"
	Body        string // extra markdown guidance (may be multi-line)
}

var Commands = []Command{reindexCmd, indexCmd, statusCmd}
```

Exact values (used verbatim in test assertions throughout):

| var | Name | Shell | Description |
| --- | --- | --- | --- |
| `reindexCmd` | `reindex` | `max-context --reindex` | `Rebuild the max-context index.` |
| `indexCmd` | `index` | `max-context --index` | `Build the max-context index for this project.` |
| `statusCmd` | `status` | `max-context --status` | `Show max-context index health, file and symbol counts.` |

---

## Task 1: Command catalog + frontmatter renderer (Claude/Cursor format)

**Files:**
- Create: `internal/setup/commands.go`
- Create: `internal/setup/commands_test.go`

- [ ] **Step 1: Write the failing test**

`internal/setup/commands_test.go`:
```go
package setup

import "testing"

func TestCommandsCatalog(t *testing.T) {
	if len(Commands) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(Commands))
	}
	names := map[string]string{}
	for _, c := range Commands {
		names[c.Name] = c.Shell
	}
	for name, shell := range map[string]string{
		"reindex": "max-context --reindex",
		"index":   "max-context --index",
		"status":  "max-context --status",
	} {
		if names[name] != shell {
			t.Errorf("command %q: want shell %q, got %q", name, shell, names[name])
		}
	}
}

func TestRenderFrontmatterCommand(t *testing.T) {
	out := renderFrontmatterCommand(reindexCmd)
	for _, want := range []string{
		"---",
		"name: reindex",
		"description: Rebuild the max-context index.",
		"max-context --reindex",
	} {
		if !contains(out, want) {
			t.Errorf("rendered command missing %q\n---\n%s", want, out)
		}
	}
}

// contains is a tiny helper to avoid importing strings in every assertion.
func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		indexOf(haystack, needle) >= 0)
}
func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/abdussamadbello/max-context/max-context-core && go test ./internal/setup/ -run 'TestCommandsCatalog|TestRenderFrontmatterCommand' -v`
Expected: FAIL — `undefined: Commands`, `undefined: renderFrontmatterCommand`, `undefined: reindexCmd`.

- [ ] **Step 3: Write minimal implementation**

`internal/setup/commands.go`:
```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/setup/ -run 'TestCommandsCatalog|TestRenderFrontmatterCommand' -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/setup/commands.go internal/setup/commands_test.go
git commit -m "feat(setup): command catalog + frontmatter renderer"
```

---

## Task 2: Claude Code command files

**Files:**
- Modify: `internal/setup/commands.go` (add `writeClaudeCommands`)
- Modify: `internal/setup/claude_code.go` (call it)
- Modify: `internal/setup/commands_test.go` (add test)

- [ ] **Step 1: Write the failing test**

Add to `internal/setup/commands_test.go`:
```go
import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetupClaudeCodeWritesCommands(t *testing.T) {
	root := t.TempDir()
	if err := setupClaudeCode(root); err != nil {
		t.Fatalf("setupClaudeCode: %v", err)
	}
	cases := map[string]string{
		"reindex.md": "max-context --reindex",
		"index.md":   "max-context --index",
		"status.md":  "max-context --status",
	}
	for file, shell := range cases {
		p := filepath.Join(root, ".claude", "commands", file)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("expected %s: %v", p, err)
		}
		if !contains(string(data), shell) {
			t.Errorf("%s missing %q", file, shell)
		}
	}
}
```
(Keep the existing `import "testing"` line consistent — merge imports into one block.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/setup/ -run TestSetupClaudeCodeWritesCommands -v`
Expected: FAIL — `.claude/commands/reindex.md` does not exist.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/setup/commands.go`:
```go
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
```

In `internal/setup/claude_code.go`, call it before the final `return appendWithMarkers(...)`. Change the end of `setupClaudeCode` from:
```go
	return appendWithMarkers(filepath.Join(root, "AGENTS.md"), "Use max-context: query_codebase, get_architecture.")
}
```
to:
```go
	if err := writeClaudeCommands(root); err != nil {
		return err
	}
	return appendWithMarkers(filepath.Join(root, "AGENTS.md"), "Use max-context: query_codebase, get_architecture.")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/setup/ -run TestSetupClaudeCodeWritesCommands -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/setup/commands.go internal/setup/claude_code.go internal/setup/commands_test.go
git commit -m "feat(setup): Claude Code command files"
```

---

## Task 3: Cursor command files

**Files:**
- Modify: `internal/setup/commands.go` (add `writeCursorCommands`)
- Modify: `internal/setup/cursor.go` (call it)
- Modify: `internal/setup/commands_test.go` (add test)

- [ ] **Step 1: Write the failing test**

Add to `internal/setup/commands_test.go`:
```go
func TestSetupCursorWritesCommands(t *testing.T) {
	root := t.TempDir()
	if err := setupCursor(root); err != nil {
		t.Fatalf("setupCursor: %v", err)
	}
	for file, shell := range map[string]string{
		"reindex.md": "max-context --reindex",
		"index.md":   "max-context --index",
		"status.md":  "max-context --status",
	} {
		p := filepath.Join(root, ".cursor", "commands", file)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("expected %s: %v", p, err)
		}
		if !contains(string(data), shell) {
			t.Errorf("%s missing %q", file, shell)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/setup/ -run TestSetupCursorWritesCommands -v`
Expected: FAIL — `.cursor/commands/reindex.md` missing.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/setup/commands.go` (Cursor shares the frontmatter format with Claude Code):
```go
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
```

In `internal/setup/cursor.go`, before the final `return appendWithMarkers(...)`, add:
```go
	if err := writeCursorCommands(root); err != nil {
		return err
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/setup/ -run TestSetupCursorWritesCommands -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/setup/commands.go internal/setup/cursor.go internal/setup/commands_test.go
git commit -m "feat(setup): Cursor command files"
```

---

## Task 4: VS Code prompt files

**Files:**
- Modify: `internal/setup/commands.go` (add `renderVSCodePrompt`, `writeVSCodePrompts`)
- Modify: `internal/setup/vscode.go` (call it)
- Modify: `internal/setup/commands_test.go` (add test)

- [ ] **Step 1: Write the failing test**

Add to `internal/setup/commands_test.go`:
```go
func TestSetupVSCodeWritesPrompts(t *testing.T) {
	root := t.TempDir()
	if err := setupVSCode(root); err != nil {
		t.Fatalf("setupVSCode: %v", err)
	}
	for file, shell := range map[string]string{
		"reindex.prompt.md": "max-context --reindex",
		"index.prompt.md":   "max-context --index",
		"status.prompt.md":  "max-context --status",
	} {
		p := filepath.Join(root, ".github", "prompts", file)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("expected %s: %v", p, err)
		}
		s := string(data)
		if !contains(s, shell) || !contains(s, "description:") {
			t.Errorf("%s missing shell %q or description frontmatter", file, shell)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/setup/ -run TestSetupVSCodeWritesPrompts -v`
Expected: FAIL — `.github/prompts/reindex.prompt.md` missing.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/setup/commands.go`. (VS Code prompt files use `.prompt.md` and frontmatter with `description` + optional `agent`; format verified against VS Code Copilot customization docs.)
```go
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
```

In `internal/setup/vscode.go`, before the final `return appendWithMarkers(...)`, add:
```go
	if err := writeVSCodePrompts(root); err != nil {
		return err
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/setup/ -run TestSetupVSCodeWritesPrompts -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/setup/commands.go internal/setup/vscode.go internal/setup/commands_test.go
git commit -m "feat(setup): VS Code prompt files"
```

---

## Task 5: Windsurf workflow files (replace inline reindex)

**Files:**
- Modify: `internal/setup/commands.go` (add `renderWindsurfWorkflow`, `writeWindsurfCommands`)
- Modify: `internal/setup/windsurf.go` (replace inline reindex workflow block)
- Modify: `internal/setup/commands_test.go` (add test)

- [ ] **Step 1: Write the failing test**

Add to `internal/setup/commands_test.go`:
```go
func TestSetupWindsurfWritesWorkflows(t *testing.T) {
	root := t.TempDir()
	if err := setupWindsurf(root); err != nil {
		t.Fatalf("setupWindsurf: %v", err)
	}
	for file, shell := range map[string]string{
		"reindex.md": "max-context --reindex",
		"index.md":   "max-context --index",
		"status.md":  "max-context --status",
	} {
		p := filepath.Join(root, ".windsurf", "workflows", file)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("expected %s: %v", p, err)
		}
		if !contains(string(data), shell) {
			t.Errorf("%s missing %q", file, shell)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/setup/ -run TestSetupWindsurfWritesWorkflows -v`
Expected: FAIL — only `reindex.md` exists today (and may lack the exact string); `index.md`/`status.md` missing.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/setup/commands.go`. (Windsurf workflows are markdown with `description` frontmatter; falls back cleanly to plain markdown body.)
```go
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
```

In `internal/setup/windsurf.go`, REMOVE the inline workflow block (the `workflowDir`, `workflowPath`, `workflowContent` lines that write `.windsurf/workflows/reindex.md`) and replace with a call before the final `appendWithMarkers`. The function should keep writing the rule file, then:
```go
	if err := writeWindsurfCommands(root); err != nil {
		return err
	}
	agentsPath := filepath.Join(root, "AGENTS.md")
	return appendWithMarkers(agentsPath, "Use max-context MCP: query_codebase, get_architecture.")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/setup/ -run TestSetupWindsurfWritesWorkflows -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/setup/commands.go internal/setup/windsurf.go internal/setup/commands_test.go
git commit -m "feat(setup): Windsurf workflows from catalog (replaces inline reindex)"
```

---

## Task 6: Codex + Antigravity skill command sections

**Files:**
- Modify: `internal/setup/commands.go` (add `renderSkillCommandsSection`)
- Modify: `internal/setup/codex.go` (append section to SKILL.md content)
- Modify: `internal/setup/antigravity.go` (append section to SKILL.md content)
- Modify: `internal/setup/commands_test.go` (add tests)

- [ ] **Step 1: Write the failing test**

Add to `internal/setup/commands_test.go`:
```go
func TestRenderSkillCommandsSection(t *testing.T) {
	out := renderSkillCommandsSection(Commands)
	for _, want := range []string{
		"## Commands",
		"max-context --reindex",
		"max-context --index",
		"max-context --status",
	} {
		if !contains(out, want) {
			t.Errorf("section missing %q", want)
		}
	}
}

func TestSetupCodexSkillHasCommands(t *testing.T) {
	root := t.TempDir()
	if err := setupCodex(root); err != nil {
		t.Fatalf("setupCodex: %v", err)
	}
	p := filepath.Join(root, ".codex", "skills", "max-context", "SKILL.md")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("expected %s: %v", p, err)
	}
	if !contains(string(data), "max-context --reindex") {
		t.Errorf("codex SKILL.md missing commands section")
	}
}

func TestSetupAntigravitySkillHasCommands(t *testing.T) {
	root := t.TempDir()
	if err := setupAntigravity(root); err != nil {
		t.Fatalf("setupAntigravity: %v", err)
	}
	p := filepath.Join(root, ".agent", "skills", "max-context", "SKILL.md")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("expected %s: %v", p, err)
	}
	if !contains(string(data), "max-context --status") {
		t.Errorf("antigravity SKILL.md missing commands section")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/setup/ -run 'TestRenderSkillCommandsSection|TestSetupCodexSkillHasCommands|TestSetupAntigravitySkillHasCommands' -v`
Expected: FAIL — `undefined: renderSkillCommandsSection`; SKILL.md files lack the command strings.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/setup/commands.go`:
```go
// renderSkillCommandsSection renders the catalog as a "## Commands" markdown
// block for editors without a native command mechanism (Codex, Antigravity).
func renderSkillCommandsSection(cmds []Command) string {
	var b strings.Builder
	b.WriteString("\n## Commands\n\n")
	for _, c := range cmds {
		fmt.Fprintf(&b, "- **%s** — %s\n  ```bash\n  %s\n  ```\n", c.Name, c.Description, c.Shell)
	}
	return b.String()
}
```

In `internal/setup/codex.go`, change the SKILL.md content so the commands section is appended. Replace the inline write:
```go
		os.WriteFile(skillPath, []byte("# Max Context\nUse query_codebase and get_architecture.\n"), 0644)
```
with:
```go
		content := "# Max Context\nUse query_codebase and get_architecture.\n" + renderSkillCommandsSection(Commands)
		os.WriteFile(skillPath, []byte(content), 0644)
```

Make the identical change in `internal/setup/antigravity.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/setup/ -run 'TestRenderSkillCommandsSection|TestSetupCodexSkillHasCommands|TestSetupAntigravitySkillHasCommands' -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/setup/commands.go internal/setup/codex.go internal/setup/antigravity.go internal/setup/commands_test.go
git commit -m "feat(setup): Codex + Antigravity skill command sections"
```

---

## Task 7: Idempotency + full-suite verification

**Files:**
- Modify: `internal/setup/commands_test.go` (add idempotency test)

- [ ] **Step 1: Write the failing test**

Add to `internal/setup/commands_test.go`:
```go
func TestSetupCommandsAreIdempotent(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 2; i++ {
		if err := setupClaudeCode(root); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	// A user edit must survive a second setup run.
	p := filepath.Join(root, ".claude", "commands", "reindex.md")
	if err := os.WriteFile(p, []byte("EDITED"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := setupClaudeCode(root); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "EDITED" {
		t.Errorf("user edit was overwritten: %q", string(data))
	}
}
```

- [ ] **Step 2: Run test to verify it fails or passes**

Run: `go test ./internal/setup/ -run TestSetupCommandsAreIdempotent -v`
Expected: PASS (the `writeIfNotExists` guard from Task 1 already provides this). If it FAILS, fix `writeIfNotExists` to honor existing files. This test locks the behavior in.

- [ ] **Step 3: Run the entire package suite**

Run: `go test ./internal/setup/ -v`
Expected: all tests PASS.

- [ ] **Step 4: Build + lint the whole project**

Run: `make build && make test && make lint`
Expected: binary builds; full `go test ./...` passes; `go vet` clean.

- [ ] **Step 5: Manual smoke check**

Run:
```bash
cd /tmp && rm -rf mc-smoke && mkdir mc-smoke && cd mc-smoke
/home/abdussamadbello/max-context/max-context-core/bin/max-context setup all
find .claude/commands .cursor/commands .github/prompts .windsurf/workflows -type f 2>/dev/null
cat .codex/skills/max-context/SKILL.md
```
Expected: command files present for Claude Code (3), Cursor (3), VS Code (3 `.prompt.md`), Windsurf (3); Codex/Antigravity SKILL.md contain a `## Commands` section. Clean up: `rm -rf /tmp/mc-smoke`.

- [ ] **Step 6: Commit**

```bash
git add internal/setup/commands_test.go
git commit -m "test(setup): command idempotency + full-suite green"
```

---

## Task 8: Update docs to reflect real cross-IDE commands

**Files:**
- Modify: `README.md` (note commands are now generated for all IDEs)

- [ ] **Step 1: Update the README**

In `/home/abdussamadbello/max-context/max-context-core/README.md`, find the Claude Code plugin section that mentions `/reindex`. Add a sentence after it:
```markdown
`max-context setup <cli>` now also generates `reindex`, `index`, and `status` commands for each editor in its native format — slash commands for Claude Code, Cursor, and VS Code Copilot; workflows for Windsurf; and a documented Commands section in the skill file for Codex and Antigravity.
```

- [ ] **Step 2: Verify the doc builds (markdown only — no build needed)**

Run: `grep -n "reindex.*index.*status" README.md`
Expected: the new line is present.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: note cross-IDE reindex/index/status commands"
```

---

## Self-Review Notes

- **Spec coverage:** command set reindex/index/status (Task 1 catalog) ✓; native-where-possible — Claude (T2), Cursor (T3), VS Code prompts (T4), Windsurf workflows (T5) ✓; fallback skill section — Codex + Antigravity (T6) ✓; shared catalog single source of truth (T1, used by all) ✓; consolidate duplicated reindex (T5 removes inline windsurf reindex) ✓; idempotent/non-destructive (T1 `writeIfNotExists`, locked by T7) ✓; tests bootstrap `internal/setup` (T1–T7) ✓; verify-items resolved during planning via Context7 (Cursor `.cursor/commands/` + `name`/`description`, VS Code `.prompt.md` + `description`/`agent`) and baked into assertions ✓.
- **Open item from spec (root `commands/reindex.md`):** left in place — it is the Claude *plugin* command; setup additionally writes project-level `.claude/commands/`. Both can coexist; no deletion needed. Noted here so the implementer doesn't remove it.
- **Type consistency:** `Command{Name,Description,Shell,Body}`, `Commands`, `reindexCmd/indexCmd/statusCmd`, `renderFrontmatterCommand`, `renderVSCodePrompt`, `renderWindsurfWorkflow`, `renderSkillCommandsSection`, `writeIfNotExists`, `writeClaudeCommands/writeCursorCommands/writeVSCodePrompts/writeWindsurfCommands` — names used identically across tasks.
- **Placeholder scan:** no TBD/TODO; every code step shows full code; every test step shows the assertion and the exact `go test -run` command.
- **Note for implementer:** `commands_test.go` accumulates across tasks — merge all `import` lines into a single block (`os`, `path/filepath`, `testing`) rather than repeating `import "testing"`; the helper funcs `contains`/`indexOf` are defined once in Task 1.
