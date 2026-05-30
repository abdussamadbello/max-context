# Cross-IDE Operational Commands — Design Spec

**Date:** 2026-05-30
**Repo:** `max-context-core` (Go MCP server + per-IDE setup writers)
**Scope:** Add operational commands across all supported IDEs via `max-context setup <cli>`.

---

## 1. Context & Goal

Today max-context ships exactly **one** user-invocable command — `/reindex`, and **only for Claude Code** (`commands/reindex.md`). Every other IDE gets MCP config + a rule/skill but no invocable commands. Users on Cursor, Windsurf, VS Code Copilot, Codex, and Antigravity have no first-class way to trigger common operations.

**Goal:** expose three **operational commands** — `reindex`, `index`, `status` — and emit them during `max-context setup <cli>` in each IDE's **native command format where one exists**, with a documented skill/rule fallback where it doesn't. Each command is a small instruction telling the agent/editor to run the matching `max-context` CLI call.

**Non-goals:**
- No tool-shortcut commands (`/find`, `/callers`, etc.) — those duplicate what the MCP tools already do automatically. Operational commands only.
- No change to the MCP server, tools, or indexer.
- No new CLI flags — commands wrap existing `--reindex` / `--index` / `--status`.

## 2. The command set

| Command | Wraps | Body guidance |
| --- | --- | --- |
| `reindex` | `max-context --reindex` | Note: if the MCP server is running, can instead `touch .max-context/.reindex-queue`. |
| `index` | `cd` into project, then `max-context --index` | Warn: indexes the current directory recursively — run from the project root, never `~` or `/`. |
| `status` | `max-context --status` | Reports index health, file and symbol counts. |

The `index` command body carries the same home-folder guard the docs use.

## 3. Architecture

A single catalog is the source of truth; per-IDE renderers translate each command into that IDE's format. This removes the existing duplication of the reindex definition (currently in both `commands/reindex.md` and inline in `windsurf.go`).

```
internal/setup/
  commands.go        NEW — canonical catalog + per-IDE render functions
  commands_test.go   NEW — table-driven tests
  claude_code.go     MOD — write .claude/commands/{reindex,index,status}.md
  cursor.go          MOD — write .cursor/commands/{reindex,index,status}.md
  windsurf.go        MOD — write .windsurf/workflows/{reindex,index,status}.md (replaces inline reindex)
  vscode.go          MOD — write .github/prompts/{reindex,index,status}.prompt.md
  codex.go           MOD — append a "Commands" section to .codex/skills/max-context/SKILL.md
  antigravity.go     MOD — append a "Commands" section to .agent/skills/max-context/SKILL.md
  setup.go           unchanged (dispatch + appendWithMarkers + ensureDir helpers reused)
```

**Catalog (`commands.go`):**
```go
type Command struct {
    Name        string // "reindex"
    Description string // one-line summary
    Shell       string // "max-context --reindex"
    Body        string // extra guidance (markdown)
}

var Commands = []Command{ reindexCmd, indexCmd, statusCmd }
```

**Renderers** (each returns file content as a string):
- `renderClaudeCommand(c Command) string` — frontmatter (`name`, `description`) + body, matching the existing `commands/reindex.md` format.
- `renderCursorCommand(c Command) string`
- `renderWindsurfWorkflow(c Command) string`
- `renderVSCodePrompt(c Command) string`
- `renderSkillCommandsSection(cmds []Command) string` — a markdown "## Commands" block appended to Codex/Antigravity SKILL.md.

Writers iterate `Commands`, render, `ensureDir`, and write **if-not-exists** (existing idempotent pattern). A render/write failure for one command is logged and does not abort `setup` (matches current `runOne` tolerance).

## 4. Per-IDE representation

| IDE | Mechanism | Destination |
| --- | --- | --- |
| Claude Code | slash commands | `.claude/commands/{name}.md` |
| Cursor | commands | `.cursor/commands/{name}.md` (keep existing rules + mcp.json) |
| Windsurf | workflows | `.windsurf/workflows/{name}.md` (consolidates current inline reindex) |
| VS Code Copilot | prompt files | `.github/prompts/{name}.prompt.md` |
| Codex | skill section | append "## Commands" to `.codex/skills/max-context/SKILL.md` |
| Antigravity | skill section | append "## Commands" to `.agent/skills/max-context/SKILL.md` |

**Format verify-items (confirm against current IDE docs during planning — these drift):**
- Cursor `.cursor/commands/` file format (frontmatter? plain markdown?).
- VS Code `.github/prompts/*.prompt.md` front-matter conventions.
- Windsurf workflow frontmatter (currently the repo writes plain `# Title` + body; confirm whether `---`/`description` frontmatter is expected).
- Claude Code command frontmatter is known from `commands/reindex.md` (`name` + `description`).

If a format can't be confirmed, fall back to plain markdown (title + description + fenced shell command), which every one of these editors renders acceptably.

## 5. Error handling

- **Non-destructive / idempotent:** write-if-not-exists for every command file; never overwrite a user-edited file. `AGENTS.md` marker logic unchanged.
- **Best-effort:** a single command-file write failure is logged; `setup` continues and still returns success for the rest (consistent with today's `runOne`).
- **No partial frontmatter ambiguity:** if a verify-item format is unconfirmed, use the plain-markdown fallback rather than guess a frontmatter schema.

## 6. Testing

New `internal/setup/commands_test.go` (the package's first tests):
- **Catalog test:** every command in `Commands` renders in every format without error and the rendered output contains the command's `Shell` string.
- **Per-IDE setup tests:** table-driven — run each IDE's setup into `t.TempDir()`, assert the expected command files exist at the right paths and contain the right shell command (e.g. `.claude/commands/reindex.md` contains `max-context --reindex`; `.windsurf/workflows/status.md` contains `max-context --status`).
- **Idempotency test:** running setup twice does not error and does not duplicate content.

Run with `make test`; `make lint` (`go vet`) must pass.

## 7. Decisions locked
- Command set: `reindex`, `index`, `status` (operational only).
- Native-where-possible + skill/rule fallback.
- Shared Go catalog (`commands.go`) as single source of truth; thin per-IDE renderers.
- Tests cover new command generation + renderers (bootstraps `internal/setup` tests); existing setup output not backfilled this round.
- Consolidate the duplicated reindex definition into the catalog.

## 8. Open items (resolve in planning)
- Exact Cursor / VS Code / Windsurf command file formats (verify-items above).
- Whether to also delete `commands/reindex.md` at repo root or keep it as the Claude *plugin* command and have setup render to `.claude/commands/` — confirm Claude Code's plugin vs project command precedence during planning.
