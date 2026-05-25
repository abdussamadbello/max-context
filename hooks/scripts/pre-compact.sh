#!/usr/bin/env bash
# PreCompact: persist a session note to .max-context/summary.md before context compaction.
set -euo pipefail
DIR="${CLAUDE_PROJECT_DIR:-.}/.max-context"
mkdir -p "$DIR"
TS=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
echo "" >> "$DIR/summary.md"
echo "--- Session preserved (pre-compact) $TS ---" >> "$DIR/summary.md"
