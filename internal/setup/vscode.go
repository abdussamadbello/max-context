package setup

import (
	"os"
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

func setupVSCode(root string) error {
	ensureDir(filepath.Join(root, ".vscode"))
	ensureDir(filepath.Join(root, ".github", "skills", "max-context"))
	// Phase 5: shared hooks for VS Code Copilot (PreToolUse, SessionStart, PreCompact)
	ensureDir(filepath.Join(root, ".github", "hooks", "scripts"))
	hooksPath := filepath.Join(root, ".github", "hooks", "hooks.json")
	if _, err := os.Stat(hooksPath); os.IsNotExist(err) {
		if err := os.WriteFile(hooksPath, []byte(vscodeHooksJSON), 0644); err != nil {
			return err
		}
	}
	writeScriptIfNotExists(filepath.Join(root, ".github", "hooks", "scripts", "session-start.sh"), sessionStartScript)
	writeScriptIfNotExists(filepath.Join(root, ".github", "hooks", "scripts", "pre-compact.sh"), preCompactScript)

	mcpPath := filepath.Join(root, ".vscode", "mcp.json")
	if _, err := os.Stat(mcpPath); os.IsNotExist(err) {
		os.WriteFile(mcpPath, []byte(`{"mcpServers":{"max-context":{"command":"max-context","args":[]}}}`), 0644)
	}
	skillPath := filepath.Join(root, ".github", "skills", "max-context", "SKILL.md")
	if _, err := os.Stat(skillPath); os.IsNotExist(err) {
		os.WriteFile(skillPath, []byte("# Max Context\nUse query_codebase and get_architecture.\n"), 0644)
	}
	return appendWithMarkers(filepath.Join(root, "AGENTS.md"), "Use max-context: query_codebase, get_architecture.")
}

func writeScriptIfNotExists(path, content string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		os.WriteFile(path, []byte(content), 0755)
	}
}
