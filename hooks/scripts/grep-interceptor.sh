#!/usr/bin/env bash
# PreToolUse (Grep): if the index is healthy, suggest query_codebase instead.
# Reads .max-context/status.json to check health. If unhealthy or missing, allows grep.
set -euo pipefail

STATUS_FILE="${CLAUDE_PROJECT_DIR:-.}/.max-context/status.json"

# If no index exists, allow grep (nothing to suggest)
if [ ! -f "$STATUS_FILE" ]; then
  echo '{"decision": "allow"}'
  exit 0
fi

HEALTHY=$(jq -r '.healthy // false' "$STATUS_FILE" 2>/dev/null)
REINDEXING=$(jq -r '.reindexInProgress // false' "$STATUS_FILE" 2>/dev/null)

# During reindex or if unhealthy, allow grep through
if [ "$HEALTHY" != "true" ] || [ "$REINDEXING" = "true" ]; then
  echo '{"decision": "allow"}'
  exit 0
fi

# Index is healthy — suggest query_codebase
echo '{"decision": "ask", "message": "The codebase is indexed. Consider using query_codebase (MCP tool) for faster, ranked search results instead of Grep."}'
