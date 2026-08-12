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

## Reporting bugs

Open an issue with: what you ran, what happened, what you expected, and your platform (OS + Go version + `max-context --version`).

## Code of conduct

Be kind. Disagree with ideas, not people. Maintainers reserve the right to remove anything that doesn't meet that bar.
