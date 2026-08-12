package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const MarkerStart = "<!-- max-context start -->"
const MarkerEnd = "<!-- max-context end -->"

// gitignoreEntry keeps the per-project index DB and artifacts under
// .max-context/ out of version control.
const gitignoreEntry = ".max-context/"

// Run configures the given CLI target (or all of them) for projectRoot and
// returns a Report describing every file it created, updated, left alone, or
// refused to touch. The caller is expected to show the report: a setup command
// that exits silently gives the user no way to tell success from a no-op.
func Run(projectRoot string, target string) (*Report, error) {
	r := NewReport(projectRoot)
	// Regardless of target, keep the index out of version control.
	_ = ensureGitignore(projectRoot, r)

	if target == "all" {
		for _, h := range harnesses {
			if err := h.apply(projectRoot, r); err != nil {
				return r, fmt.Errorf("setup %s: %w", h.Name, err)
			}
		}
		return r, nil
	}

	h, ok := lookupHarness(target)
	if !ok {
		return nil, fmt.Errorf("unknown setup target %q; known targets: %s, all",
			target, strings.Join(HarnessNames(), ", "))
	}
	return r, h.apply(projectRoot, r)
}

// appendWithMarkers appends content to filePath inside sentinel markers, once.
// Re-running is a no-op, so it is safe on a file the user also edits by hand.
func appendWithMarkers(filePath, content string, r *Report) error {
	existing, err := os.ReadFile(filePath)
	s := string(existing)
	if strings.Contains(s, MarkerStart) {
		r.unchanged(filePath, "max-context block already present")
		return nil
	}
	created := os.IsNotExist(err)
	s = strings.TrimRight(s, "\n")
	if s != "" {
		s += "\n"
	}
	s += "\n" + MarkerStart + "\n" + content + "\n" + MarkerEnd + "\n"
	if err := os.WriteFile(filePath, []byte(strings.TrimLeft(s, "\n")), 0644); err != nil {
		return err
	}
	if created {
		r.created(filePath, "")
	} else {
		r.updated(filePath, "appended max-context block")
	}
	return nil
}

func ensureDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}

// ensureGitignore appends gitignoreEntry to the project's .gitignore unless
// .max-context is already ignored. Creates the file if absent. Idempotent.
func ensureGitignore(root string, r *Report) error {
	path := filepath.Join(root, ".gitignore")
	existing, readErr := os.ReadFile(path)
	for _, line := range strings.Split(string(existing), "\n") {
		switch strings.TrimSpace(line) {
		case ".max-context/", ".max-context", "/.max-context/", "/.max-context":
			r.unchanged(path, ".max-context already ignored")
			return nil // already ignored in some accepted form
		}
	}
	s := string(existing)
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	s += gitignoreEntry + "\n"
	if err := os.WriteFile(path, []byte(s), 0644); err != nil {
		return err
	}
	if os.IsNotExist(readErr) {
		r.created(path, "")
	} else {
		r.updated(path, "ignored .max-context/")
	}
	return nil
}
