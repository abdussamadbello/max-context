# Distribution checklist

Pre-release tasks for publishing max-context to the MCP ecosystem. Items are
ordered: nothing below the first section makes sense until the owner question
is settled.

## 0. Decide the canonical GitHub owner (BLOCKER)

Every reference in this repo (go.mod module path, npm repository URL,
`.goreleaser.yml` owner, `scripts/install.sh`, `npm-package/scripts/postinstall.js`,
README links, `server.json`, Homebrew formula) points at
**`github.com/maxcontext/max-context`**, but the working remote is
**`abdussamadbello/max-context`**. Pick one:

- **Create the `maxcontext` GitHub org** (and the `@maxcontext` npm scope) and
  push there — zero code changes; or
- **Rename everywhere to `abdussamadbello`** — global replace of the module
  path and the references above, plus registry namespace
  `io.github.abdussamadbello/max-context` in `server.json` and
  `npm-package/package.json` (`mcpName`).

The MCP registry namespace is verified against the GitHub owner
(`io.github.<owner>/...`), so `server.json` cannot be published until this
matches reality.

## 1. npm

- [ ] Publish `@maxcontext/cli` (the postinstall script downloads the platform
      binary from GitHub Releases — a release must exist first).
- [ ] `mcpName` in `npm-package/package.json` must equal the `name` in
      `server.json` (registry validation marker). Currently:
      `io.github.maxcontext/max-context`.

## 2. GitHub release (goreleaser)

- [ ] `goreleaser release` builds 6 platforms and `checksums.txt`.
- [ ] Refresh `Formula/max-context.rb` SHA256 placeholders from
      `checksums.txt` and push to the Homebrew tap.

## 3. Official MCP Registry

- [ ] Install `mcp-publisher`, then from the repo root:
      `mcp-publisher init` (already have `server.json`),
      `mcp-publisher login github`, `mcp-publisher publish`.
- [ ] Verify the `registryType` enum in the current schema if adding a direct
      binary-download package entry alongside npm.
- Listing propagates to aggregators (Glama, PulseMCP, mcp.so) that crawl the
  registry. The GitHub MCP Registry is curated separately and oriented toward
  remote servers; don't block on it.

## 4. Docker

- [ ] `docker build -t maxcontext/max-context .` (CGO build; image needs glibc
      and git — see Dockerfile comments).
- [ ] Push to GHCR/Docker Hub; optionally add a `dockers:` stanza to
      `.goreleaser.yml` so releases publish images automatically.
- [ ] Submit to the Docker MCP Catalog (PR to `docker/mcp-registry`) for the
      verified-tier trust badge.

## 5. Claude Code plugin marketplace

- [ ] The plugin (`.claude-plugin/`) already ships hooks, skills, and commands.
      Submit to `anthropics/claude-plugins-official` for default-marketplace
      placement; their review requires tool annotations
      (readOnlyHint/destructiveHint — added in the MCP layer) and a working
      `npx`/binary install path.

## 6. README install surfaces

- [ ] Verify the one-click deeplink formats at release time — they drift:
      - Cursor: `https://cursor.com/en/install-mcp?name=max-context&config=<base64 of {"command":"max-context"}>`
      - VS Code: `vscode:mcp/install?<url-encoded {"name":"max-context","command":"max-context"}>`
- [ ] Add the npm/release/registry badges once the packages exist.
- [ ] PR to `punkpeye/awesome-mcp-servers`.
