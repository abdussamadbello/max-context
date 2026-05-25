package watcher

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitChangedFiles returns list of changed file paths between HEAD@{1} and HEAD
// (e.g. after branch switch, pull, merge, rebase).
func GitChangedFiles(repoRoot string) ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", "HEAD@{1}", "HEAD")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		// Fallback: if reflog fails (e.g. fresh clone), return empty
		return nil, nil
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, filepath.ToSlash(line))
		}
	}
	return paths, nil
}

// HandleGitEvent processes a git HEAD/index change. If fewer than maxIncremental
// files changed, sends each to the reindex channel for sequential processing.
// Otherwise, writes all paths to .reindex-queue for bulk processing.
func HandleGitEvent(repoRoot string, reindexCh chan<- string, maxIncremental int) error {
	files, err := GitChangedFiles(repoRoot)
	if err != nil {
		return fmt.Errorf("git changed files: %w", err)
	}
	if len(files) == 0 {
		return nil
	}

	if len(files) < maxIncremental {
		for _, f := range files {
			reindexCh <- filepath.Join(repoRoot, f)
		}
	} else {
		// Bulk: write to .reindex-queue for watcher to pick up as full reindex
		queuePath := filepath.Join(repoRoot, ".max-context", ".reindex-queue")
		f, err := os.OpenFile(queuePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("open reindex queue: %w", err)
		}
		defer f.Close()
		for _, path := range files {
			fmt.Fprintln(f, filepath.Join(repoRoot, path))
		}
	}
	return nil
}
