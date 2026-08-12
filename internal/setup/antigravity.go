package setup

import (
	"path/filepath"
)

func setupAntigravity(root string, r *Report) error {
	skillPath := filepath.Join(root, ".agent", "skills", "max-context", "SKILL.md")
	content := "# Max Context\nUse query_codebase and get_architecture.\n" + renderSkillCommandsSection(Commands)
	if err := writeFileIfAbsent(skillPath, content, 0644, r); err != nil {
		return err
	}
	return appendWithMarkers(filepath.Join(root, "AGENTS.md"), "Use max-context: query_codebase, get_architecture.", r)
}
