package bench

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PathFilter decides which repo-relative paths are in scope. Both baselines and
// max-context must see the same file set, or the comparison measures the
// difference in what was searched rather than the difference in how.
type PathFilter interface {
	Match(relPath string) bool // true = excluded
}

// NaiveBaseline simulates an unoptimized agent: for each term, do a recursive
// grep across all files under root, then read every matching file in full.
// Returns the cl100k_base token count of everything the LLM would see.
func NaiveBaseline(root string, terms []string, filter PathFilter) (int, error) {
	c, err := NewCounter()
	if err != nil {
		return 0, err
	}
	var total int
	for _, term := range terms {
		grepLines, matchedFiles, err := recursiveGrep(root, term, false, filter)
		if err != nil {
			return 0, err
		}
		total += c.Count(strings.Join(grepLines, "\n"))
		seen := map[string]bool{}
		for _, m := range matchedFiles {
			if seen[m.file] {
				continue
			}
			seen[m.file] = true
			body, err := os.ReadFile(m.file)
			if err != nil {
				continue
			}
			total += c.Count(string(body))
		}
	}
	return total, nil
}

// SkilledBaseline simulates a careful agent: grep with sensible filters, then
// read only ±20 lines around each match, deduplicating reads.
func SkilledBaseline(root string, terms []string, filter PathFilter) (int, error) {
	c, err := NewCounter()
	if err != nil {
		return 0, err
	}
	var total int
	read := map[string]bool{}
	for _, term := range terms {
		grepLines, matches, err := recursiveGrep(root, term, true, filter)
		if err != nil {
			return 0, err
		}
		total += c.Count(strings.Join(grepLines, "\n"))
		for _, m := range matches {
			body, err := windowAroundLine(m.file, m.line, 20)
			if err != nil {
				continue
			}
			key := fmt.Sprintf("%s:%d", m.file, m.line/40)
			if read[key] {
				continue
			}
			read[key] = true
			total += c.Count(body)
		}
	}
	return total, nil
}

type match struct {
	file string
	line int
}

func recursiveGrep(root, term string, skipNoise bool, filter PathFilter) (allLines []string, files []match, err error) {
	noiseDirs := map[string]bool{
		"node_modules": true, ".git": true, "dist": true, "build": true,
		"vendor": true, ".max-context": true, "bin": true,
	}
	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if info.IsDir() {
			base := info.Name()
			if skipNoise && noiseDirs[base] {
				return filepath.SkipDir
			}
			if base == "." || base == ".." {
				return nil
			}
			if strings.HasPrefix(base, ".") && len(base) > 1 && base != ".github" {
				return filepath.SkipDir
			}
			// Excluded by the same rules max-context indexes under.
			if filter != nil && rel != "." && filter.Match(rel+"/") {
				return filepath.SkipDir
			}
			return nil
		}
		if filter != nil && filter.Match(rel) {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		// Skip binaries the way grep does. Without this the naive baseline
		// tokenized compiled artifacts under bin/ and dist/ — tens of millions of
		// tokens of machine code that no agent would ever put in a context window,
		// which made the naive comparison meaningless rather than merely
		// unflattering.
		if isBinary(f) {
			return nil
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		ln := 0
		for scanner.Scan() {
			ln++
			line := scanner.Text()
			if strings.Contains(line, term) {
				allLines = append(allLines, fmt.Sprintf("%s:%d:%s", rel, ln, line))
				files = append(files, match{file: path, line: ln})
			}
		}
		return nil
	})
	return allLines, files, walkErr
}

func windowAroundLine(path string, line, radius int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	low, high := line-radius, line+radius
	var b strings.Builder
	ln := 0
	for scanner.Scan() {
		ln++
		if ln < low {
			continue
		}
		if ln > high {
			break
		}
		b.WriteString(scanner.Text())
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// isBinary reports whether the file looks like binary content, using the same
// heuristic as grep: a NUL byte in the first block. Rewinds so the caller can
// still read from the start.
func isBinary(f *os.File) bool {
	var buf [8000]byte
	n, err := f.Read(buf[:])
	if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
		return true // cannot rewind; treat as unreadable
	}
	if err != nil && n == 0 {
		return false
	}
	return bytes.IndexByte(buf[:n], 0) >= 0
}
