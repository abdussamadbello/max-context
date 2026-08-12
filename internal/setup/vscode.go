package setup

import (
	"path/filepath"
)

// VS Code hooks (Phase 5): same behavior as Claude Code plugin, using .github/hooks and CLAUDE_PROJECT_DIR.
const vscodeHooksJSON = `{
  "description": "Max-context: suggest query_codebase, inject architecture at session start, persist summary before compact.",
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Grep|Read",
        "hooks": [
          {
            "type": "prompt",
            "prompt": "The user is about to run a tool that searches or reads files (Grep or Read). If the intent is to search the codebase for symbols, definitions, or usage, suggest using the MCP tool query_codebase instead. Return permissionDecision: 'ask' and a short systemMessage suggesting: 'Consider using query_codebase for codebase search instead of Grep/Read.' If the intent is clearly a single-file read or a non-codebase grep, return permissionDecision: 'allow'."
          }
        ]
      }
    ],
    "SessionStart": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "bash ${CLAUDE_PROJECT_DIR}/.github/hooks/scripts/session-start.sh",
            "timeout": 10
          }
        ]
      }
    ],
    "PreCompact": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "bash ${CLAUDE_PROJECT_DIR}/.github/hooks/scripts/pre-compact.sh",
            "timeout": 15
          }
        ]
      }
    ]
  }
}
`

const sessionStartScript = `#!/usr/bin/env bash
set -euo pipefail
DIR="${CLAUDE_PROJECT_DIR:-.}/.max-context"
if [[ -d "$DIR" ]]; then
  if [[ -f "$DIR/summary.md" ]]; then echo "--- max-context summary.md ---"; cat "$DIR/summary.md"; echo ""; fi
  if [[ -f "$DIR/architecture.md" ]]; then echo "--- max-context architecture.md ---"; cat "$DIR/architecture.md"; fi
fi
`

const preCompactScript = `#!/usr/bin/env bash
set -euo pipefail
DIR="${CLAUDE_PROJECT_DIR:-.}/.max-context"
mkdir -p "$DIR"
TS=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
echo "" >> "$DIR/summary.md"
echo "--- Session preserved (pre-compact) $TS ---" >> "$DIR/summary.md"
`

func setupVSCode(root string, r *Report) error {
	// Phase 5: shared hooks for VS Code Copilot (PreToolUse, SessionStart, PreCompact)
	hooksPath := filepath.Join(root, ".github", "hooks", "hooks.json")
	if err := writeFileIfAbsent(hooksPath, vscodeHooksJSON, 0644, r); err != nil {
		return err
	}
	scripts := filepath.Join(root, ".github", "hooks", "scripts")
	if err := writeFileIfAbsent(filepath.Join(scripts, "session-start.sh"), sessionStartScript, 0755, r); err != nil {
		return err
	}
	if err := writeFileIfAbsent(filepath.Join(scripts, "pre-compact.sh"), preCompactScript, 0755, r); err != nil {
		return err
	}
	if err := mergeMCPConfig(filepath.Join(root, ".vscode", "mcp.json"), "mcpServers", r); err != nil {
		return err
	}
	skillPath := filepath.Join(root, ".github", "skills", "max-context", "SKILL.md")
	if err := writeFileIfAbsent(skillPath, "# Max Context\nUse query_codebase and get_architecture.\n", 0644, r); err != nil {
		return err
	}
	if err := writeVSCodePrompts(root, r); err != nil {
		return err
	}
	return appendWithMarkers(filepath.Join(root, "AGENTS.md"), "Use max-context: query_codebase, get_architecture.", r)
}
