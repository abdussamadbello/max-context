# Maximum Context

Maximum Context is a **Go-based MCP (Model Context Protocol) server** that gives AI coding assistants deep, always-current awareness of a codebase. It indexes a project's structure, symbols, dependencies, and architecture, then exposes that knowledge through a focused, **decisive MCP tool interface**.

## Why max-context

AI coding agents waste 30–60% of their context window grepping and re-reading files to figure out where things are. max-context pre-computes that understanding and serves it on demand.

- **Always current** — file changes reflected in < 2 seconds via OS-native watchers
- **No LLM at index time** — deterministic tree-sitter parsing; no API keys, no token spend on indexing
- **Minimal surface, no breaking changes** — a small set of focused MCP tools, stable schema

See [`BENCHMARK.md`](./BENCHMARK.md) for per-tool-call token savings vs naive and skilled Grep+Read baselines. The current skilled-baseline headline is **29.5× fewer tokens per deterministic query on max-context's own codebase**.

For *agent-session* quality and cost — does an LLM actually do better with these tools? — see the causal A/B in [`experiments/eval/FINDINGS.md`](./experiments/eval/FINDINGS.md) (same model, same tasks, only the toolset differs; blind different-model judge; pre-registered). On the tasks measured so far it finds **equal answer quality to a skilled grep agent, at materially lower cost** — up to 94% fewer tokens (≈90% in aggregate) on call-graph / "what-calls-this" questions — plus a structural edge on **aliased imports** (`from m import f as g; g()`), where text search can't connect the call site to the definition and max-context can. Honest scope: small sample (1 repo family, 1 replicate); quality is a *tie*, not a win, except the alias case.

## How it works

```
                                  ┌─────────────────────────────────┐
                                  │       max-context binary        │
    ┌─────────────┐ fsnotify      │   ┌─────────────────────────┐   │
    │ Codebase    │───────────────┼─▶ │   tree-sitter parser    │   │
    └─────────────┘               │   └────────────┬────────────┘   │
                                  │                ▼                │
                                  │   ┌─────────────────────────┐   │
                                  │   │SQLite FTS5 + call graph │   │
                                  │   └────────────┬────────────┘   │
    ┌─────────────┐               │                ▼                │
    │ AI CLI / IDE│ stdio MCP     │   ┌─────────────────────────┐   │
    └─────────────┘◀──────────────┼───│  MCP server · 5 tools   │   │
                                  │   └─────────────────────────┘   │
                                  └─────────────────────────────────┘
```

Tree-sitter parses every supported language deterministically. Symbols and call edges land in a SQLite database with FTS5 indexes on function/type names. Recursive CTEs power call-chain and impact queries. **No LLM is involved at index time** — the host CLI's model does all the reasoning, with max-context feeding it pre-computed structure.

## Features

- **Focused MCP tools**: `get_definition` (exact-name, canonical definition), `query_codebase` (BM25 fuzzy symbol search with explicit next action), `get_call_chain` (recursive caller/callee traversal), `get_impact` (change blast radius), `get_architecture` (pre-computed project summary)
- **Real-time index**: File watcher keeps the index current within 2 seconds of changes
- **Multi-language**: TypeScript, JavaScript, Python, Go, Rust, Java (and more via Tree-sitter)
- **Universal CLI support**: One `max-context setup <cli>` configures Claude Code, VS Code Copilot, Codex CLI, Antigravity, Cursor, and Windsurf

## Install

Pick one:

| Method | Command |
|--------|---------|
| **Homebrew** (macOS/Linux) | `brew tap maxcontext/tap && brew install maxcontext/tap/max-context` |
| **npm** (global CLI) | `npm install -g @maxcontext/cli` |
| **Install script** | `curl -fsSL https://raw.githubusercontent.com/maxcontext/max-context/main/scripts/install.sh \| sh` |
| **GitHub Release** | Download the binary for your OS/arch from [Releases](https://github.com/maxcontext/max-context/releases); verify with `checksums.txt` (SHA256). |
| **From source** | `make build` then `make install` (copies to `~/.local/bin`) or `make install-path` (install + add that dir to your shell PATH). On Windows, `make install-path` installs to `%LOCALAPPDATA%\max-context\bin` and prints instructions to add it to your user PATH. |

> [!NOTE]
> Ensure the binary is on your `PATH` after install.
> - **macOS/Linux:** `make install-path` appends `~/.local/bin` to your `PATH` in `.bashrc`, `.zshrc`, or `.profile` if it isn't already there — run `source ~/.bashrc` (or your rc file) to apply.
> - **Windows:** add the install directory to your user PATH (Settings → Environment Variables), or use the PowerShell one-liner printed by `make install-path`.
> - The install script defaults to `~/.local/bin`; set `MAX_CONTEXT_INSTALL_DIR` to override.

## Quick Start

```bash
# Build from source (optional)
make build

# Full index (builds index and starts watcher)
max-context --index

# MCP server mode (stdio; used by IDEs)
max-context

# Status
max-context --status

# Setup for a specific CLI
max-context setup cursor
max-context setup all
```

## Quickstart by CLI

After installing `max-context` and ensuring it’s on your PATH:

| CLI | Steps |
|-----|--------|
| **Claude Code** | 1. Add MCP server (e.g. `max-context setup claude-code` or put `max-context` in `.mcp.json`). 2. Install plugin: `/plugin marketplace add <path-to-repo>` then `/plugin install max-context@max-context-local` (see [Claude Code Plugin](#claude-code-plugin) below). 3. In project: `max-context --index` then start Claude Code. |
| **VS Code Copilot** | 1. `max-context setup vscode` (writes `.vscode/mcp.json`, `.github/hooks/`, skills). 2. `max-context --index` in project. 3. Open VS Code; MCP and hooks load. |
| **Codex CLI** | 1. `max-context setup codex` (adds MCP to `.codex/config.toml`, skill to `.codex/skills/max-context/`). 2. `max-context --index`. 3. Use Codex in the project. |
| **Antigravity** | 1. `max-context setup antigravity` (MCP config, `.agent/skills/max-context/`, rules). 2. `max-context --index`. 3. Run Antigravity in the project. |
| **Cursor** | 1. `max-context setup cursor` (`.cursor/mcp.json`, `.cursor/rules/max-context.md`, AGENTS.md). 2. `max-context --index`. 3. Use Cursor; MCP and rules apply. |
| **Windsurf** | 1. `max-context setup windsurf` (user MCP config, `.windsurf/rules/max-context.md`, workflows, AGENTS.md). 2. `max-context --index`. 3. Use Windsurf in the project. |

## Commands

| Flag / Subcommand | Description |
|-------------------|-------------|
| (none) | Run MCP server on stdin/stdout |
| `--index` | Build full index and start file watcher |
| `--reindex` | Force full rebuild of index |
| `--status` | Report index health, file count, symbol count |
| `--watch` | Start only the file watcher |
| `--version` | Print version and exit |
| `setup <cli>` | Generate config for claude-code, vscode, codex, antigravity, cursor, windsurf, or all |

## Claude Code Plugin

When used as a **Claude Code plugin** (install from this repo), you get:

- **PreToolUse hook** — Suggests using `query_codebase` instead of Grep/Read for codebase search
- **SessionStart hook** — Injects `.max-context/summary.md` and `architecture.md` into context
- **PreCompact hook** — Appends a session-preserved note to `summary.md` before context compaction
- **/reindex** — Slash command to run `max-context --reindex` or touch `.max-context/.reindex-queue`

`max-context setup <cli>` also generates `reindex`, `index`, and `status` commands for each editor in its native format — slash commands for Claude Code (`.claude/commands/`), Cursor (`.cursor/commands/`), and VS Code Copilot (`.github/prompts/*.prompt.md`); workflows for Windsurf (`.windsurf/workflows/`); and a documented **Commands** section in the skill file for Codex and Antigravity.

**Install (local):** This repo is a local marketplace. In Claude Code, run:
1. **Add marketplace:** `/plugin marketplace add ./max-context` — use the path to the **repo root** (e.g. `./max-context`, `~/max-context`, or `/home/.../max-context`). Valid formats: `./path`, `owner/repo`, or `https://...`.
2. **Install plugin:** `/plugin install max-context@max-context-local`

Ensure the `max-context` binary is on your PATH so the MCP server can start. The running server watches `.max-context/.reindex-queue`; writing that file triggers a full reindex in the background.

## VS Code Copilot Hooks

Run `max-context setup vscode` to create `.github/hooks/hooks.json` and `.github/hooks/scripts/` (SessionStart, PreCompact, PreToolUse) so VS Code Copilot gets the same grep-interception and session-start context as Claude Code. The hook format is shared; script paths use `${CLAUDE_PROJECT_DIR}/.github/hooks/scripts/`. In this repo, the same hook definitions live under `hooks/` (Claude Code plugin) and `.github/hooks/` (VS Code / project).

## MCP Resources

The server advertises the `resources` capability and exposes two read-only resources:

| URI | Description |
|-----|-------------|
| `maxcontext://project/summary` | Pre-computed project summary (text/markdown) |
| `maxcontext://project/architecture` | Tech stack, directory map, entry points (text/markdown) |

IDEs that support MCP Resources (e.g. VS Code Copilot) can list and read these in the chat panel.

## Project Layout

- `cmd/max-context/` — Entry point and flag parsing
- `internal/config/` — CLI flags and `.max-context/config.json`
- `internal/db/` — SQLite schema, migrations, FTS5
- `internal/indexer/` — Tree-sitter parsing, full/incremental index
- `internal/watcher/` — fsnotify file watcher, debounce, git-aware invalidation
- `internal/mcp/` — JSON-RPC 2.0 server (stdio)
- `internal/tools/` — the MCP tools: `get_definition`, `query_codebase`, `get_call_chain`, `get_impact`, `get_architecture`
- `internal/artifacts/` — `architecture.md`, `summary.md`, `status.json`
- `internal/setup/` — Per-CLI config writers
- `pkg/treesitter/` — Tree-sitter bindings and language queries
- `.claude-plugin/` — Claude Code plugin + marketplace manifests (`source: "./"` — the repo root is the plugin)
- `hooks/` — Claude Code plugin hooks (PreToolUse, SessionStart, PreCompact)
- `commands/` — Plugin slash command (e.g. `/reindex`)
- `skills/` — Plugin skills (`index-codebase`, `show-call-chain`)
- `templates/` — Per-CLI skill/rules templates emitted by `max-context setup`
- `npm-package/` — npm wrapper `@maxcontext/cli` with postinstall binary download + SHA256
- `scripts/install.sh` — Install script for curl\|sh
- `Formula/` — Homebrew formula template; update SHA256 from `checksums.txt` after each release

## Requirements

- Go 1.22+
- CGO enabled (for Tree-sitter grammars)

## License

Licensed under the [Apache License, Version 2.0](LICENSE). See [NOTICE](NOTICE) for attribution requirements when redistributing.

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md).
