---
name: index-codebase
description: Use this skill when the user asks to "index the codebase", "build the index", "rebuild the index", "reindex", "set up max-context", or when query_codebase returns an "index not ready" error. Also use when the user switches branches or pulls new code and wants fresh search results.
---

# Index Codebase

Build or rebuild the max-context code intelligence index for the current project. This parses all source files with Tree-sitter, extracts functions, types, call edges, and imports, and stores them in a SQLite database with FTS5 full-text search.

## When to Run

- First time setting up max-context in a project
- After switching branches or pulling significant changes
- When `query_codebase` returns "index not ready"
- When the user explicitly asks to reindex

## Steps

1. Run the indexer from the project root:

```bash
max-context --index
```

This will:
- Scan all source files (respecting `.gitignore` and `.contextignore`)
- Parse each file with Tree-sitter to extract symbols
- Build the call graph (function → function edges)
- Create FTS5 search indexes
- Generate `.max-context/architecture.md` and `.max-context/summary.md`
- Write `.max-context/status.json` with health info

2. Verify the index was built:

```bash
max-context --status
```

3. If the MCP server is already running, you can trigger incremental reindex by writing to the queue:

```bash
touch .max-context/.reindex-queue
```

## After Indexing

Once indexed, use the MCP tools:
- `query_codebase` — Search for functions, types, or concepts by keyword
- `get_call_chain` — Trace who calls a function and what it calls
- `get_architecture` — Get the project structure summary

## Configuration

Create `.max-context/config.json` to customize:

```json
{
  "languages": ["ts", "tsx", "js", "py", "go", "rs"],
  "include": ["src/", "lib/"],
  "exclude": ["vendor/", "generated/"],
  "maxFileSize": 1048576
}
```

Create `.contextignore` (gitignore syntax) to exclude additional paths from indexing.
