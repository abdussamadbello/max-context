#!/usr/bin/env bash
# UserPromptSubmit: compile a token-budgeted context package for the prompt and
# inject it, so the agent starts holding the evidence instead of searching for
# it. This is the hook form of `max-context context`.
#
# Opt-in. The compiler is experimental and spends budget on every prompt, so it
# stays off unless MAX_CONTEXT_AUTO_CONTEXT=1 — the same gate the CLI command
# documents. Every failure path exits 0 printing nothing: a hook that breaks the
# session it decorates is worse than no hook.
set -uo pipefail

if [ "${MAX_CONTEXT_AUTO_CONTEXT:-0}" != "1" ]; then exit 0; fi
if ! command -v max-context >/dev/null 2>&1; then exit 0; fi
if ! command -v jq >/dev/null 2>&1; then exit 0; fi

ROOT="${CLAUDE_PROJECT_DIR:-.}"
STATUS_FILE="$ROOT/.max-context/status.json"
if [ ! -f "$STATUS_FILE" ]; then exit 0; fi

# A missing, unhealthy, or rebuilding index compiles misleading evidence. Stay
# silent and let the agent search normally, exactly as grep-interceptor does.
HEALTHY=$(jq -r '.healthy // false' "$STATUS_FILE" 2>/dev/null || echo false)
REINDEXING=$(jq -r '.reindexInProgress // false' "$STATUS_FILE" 2>/dev/null || echo true)
if [ "$HEALTHY" != "true" ] || [ "$REINDEXING" = "true" ]; then exit 0; fi

PROMPT=$(jq -r '.prompt // empty' 2>/dev/null || echo "")
# Continuations ("yes", "go on", "fix it") name nothing retrievable; compiling
# for them spends the budget on noise.
if [ "${#PROMPT}" -lt 24 ]; then exit 0; fi

BUDGET="${MAX_CONTEXT_CONTEXT_BUDGET:-2000}"
PACKAGE=$(cd "$ROOT" && max-context context --task "$PROMPT" --budget "$BUDGET" 2>/dev/null || echo "")
if [ -z "$PACKAGE" ]; then exit 0; fi

echo "--- max-context compiled context (cl100k_base budget: ${BUDGET}) ---"
echo "$PACKAGE"
