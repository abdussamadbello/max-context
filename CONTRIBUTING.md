# Contributing to max-context

Thanks for your interest. This project is licensed under [Apache 2.0](LICENSE), and contributions are accepted under the same terms.

## Quick start

```bash
git clone https://github.com/maxcontext/max-context
cd max-context
make build         # builds bin/max-context
make test          # runs the Go test suite
make install       # installs to $HOME/.local/bin (override with PREFIX=)
```

You need Go 1.22+ and a C toolchain (CGO is required for the tree-sitter bindings; the SQLite driver is pure Go).

## Submitting changes

1. Open an issue first for anything non-trivial — saves rework if the direction isn't a fit.
2. Branch from `main`, keep commits focused, write descriptive messages.
3. Run `make test` and `make lint` before opening a PR.
4. By submitting, you agree your contribution is licensed under Apache 2.0 (see Section 5 of [LICENSE](LICENSE)).

## Project layout

- `cmd/max-context/` — CLI entrypoint
- `internal/` — private Go packages (indexer, MCP server, tools, watcher, etc.)
- `pkg/treesitter/` — tree-sitter bindings (public API)
- `.claude-plugin/`, `commands/`, `hooks/`, `skills/`, `templates/` — Claude Code plugin assets (the repo root is the plugin)
- `benchmark/` — token-savings benchmark harness + question sets
- `docs/` — public benchmark transcripts and screenshots

See [README.md](README.md) for the architectural overview.

## Adding an agent harness

Harnesses are entries in `harnesses` in [`internal/setup/harness.go`](internal/setup/harness.go),
not new files. A new one needs:

- `Name` — the `max-context setup <name>` target
- `MCPConfig` — where that harness reads its MCP server map, relative to the project root
- `ServersKey` — the JSON key holding the server map, if it isn't `mcpServers`
- `GuidancePath` / `Guidance` — the skill or rules file that tells the agent to use the tools
- `Commands` / `CommandsDir` — how it wants the reindex/index/status commands written
- `Format` — `FormatYAML` if the config is YAML rather than JSON (Hermes)
- `EntryStyle` — `EntryTypedArgv` if a server is `{"type":"local","command":["max-context"]}` rather than `{"command":"max-context","args":[]}` (opencode)
- `HomeRelative` — if the harness only has a global config, so `MCPConfig` resolves against `$HOME` (Hermes)
- `InstructionsKey` / `NoAgentsMD` — for harnesses that discover guidance through a config array rather than AGENTS.md (opencode)
- `Extra` — only for genuine one-offs (VS Code's hook scripts are the sole current case)

A harness with no MCP support at all (pi) simply leaves `MCPConfig` empty: it
gets the skill and the AGENTS.md block, and drives the tools through the
one-shot CLI subcommands.

Tests that touch a home-relative harness must call `fakeHome(t)` so the suite
never writes to a real `~/.hermes`.

`TestEveryHarnessConfiguresACleanProject` and `TestEveryHarnessIsIdempotent` pick
up the new entry automatically. Get `ServersKey` right: writing the wrong key
produces a config the harness silently ignores, which looks exactly like setup
having worked.

## Tool-definition budget

MCP tool schemas are re-sent on every request, so their size is a per-turn cost
in every session that loads the server. `TestToolSchemaBudget` fails if they
grow past the budget — trim a description rather than raising it, and keep the
cross-tool steering (`TestSchemasKeepCrossToolSteering` guards the parts the A/B
runs tuned).

## Reporting bugs

Open an issue with: what you ran, what happened, what you expected, and your platform (OS + Go version + `max-context --version`).

## Code of conduct

Be kind. Disagree with ideas, not people. Maintainers reserve the right to remove anything that doesn't meet that bar.
