#!/usr/bin/env bash
# SessionStart: inject .max-context/architecture.md and summary.md into context (stdout).
set -euo pipefail
DIR="${CLAUDE_PROJECT_DIR:-.}/.max-context"
if [[ -d "$DIR" ]]; then
  if [[ -f "$DIR/summary.md" ]]; then
    echo "--- max-context summary.md ---"
    cat "$DIR/summary.md"
    echo ""
  fi
  if [[ -f "$DIR/architecture.md" ]]; then
    echo "--- max-context architecture.md ---"
    cat "$DIR/architecture.md"
  fi
fi
