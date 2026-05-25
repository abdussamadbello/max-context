#!/usr/bin/env bash
# PostToolUse (Write/Edit): notify max-context MCP server to reindex the changed file.
# Writes the file path to .max-context/.reindex-queue which the watcher picks up.
#
# Hook contract: Claude Code sends a JSON payload on STDIN, shaped roughly like:
#   {
#     "session_id": "...",
#     "tool_name": "Write" | "Edit" | ...,
#     "tool_input": { "file_path": "/abs/path.ts", ... }
#   }
# (NotebookEdit uses "notebook_path"; this hook only matches Write|Edit per hooks.json.)
set -euo pipefail

# Read the entire hook payload from stdin.
INPUT=$(cat)

if ! command -v jq >/dev/null 2>&1; then
  echo "post-write-reindex.sh: jq not found on PATH; skipping reindex queue" >&2
  FILE_PATH=""
else
  FILE_PATH=$(printf '%s' "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)
  if [ -z "$FILE_PATH" ] && [ -n "$INPUT" ]; then
    echo "post-write-reindex.sh: no .tool_input.file_path in hook payload" >&2
  fi
fi

if [ -z "$FILE_PATH" ]; then
  exit 0
fi

DIR="${CLAUDE_PROJECT_DIR:-.}/.max-context"
if [ ! -d "$DIR" ]; then
  exit 0
fi

echo "$FILE_PATH" >> "$DIR/.reindex-queue"
