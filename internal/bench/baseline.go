package bench

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NaiveBaseline simulates an unoptimized agent: for each term, do a recursive
// grep across all files under root, then read every matching file in full.
// Returns the cl100k_base token count of everything the LLM would see.
func NaiveBaseline(root string, terms []string) (int, error) {
	c, err := NewCounter()
	if err != nil {
		return 0, err
	}
	var total int
	for _, term := range terms {
		grepLines, matchedFiles, err := recursiveGrep(root, term, false)
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
func SkilledBaseline(root string, terms []string) (int, error) {
	c, err := NewCounter()
	if err != nil {
		return 0, err
	}
	var total int
	read := map[string]bool{}
	for _, term := range terms {
		grepLines, matches, err := recursiveGrep(root, term, true)
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

func recursiveGrep(root, term string, skipNoise bool) (allLines []string, files []match, err error) {
	noiseDirs := map[string]bool{
		"node_modules": true, ".git": true, "dist": true, "build": true,
		"vendor": true, ".max-context": true, "bin": true,
	}
	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
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
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		ln := 0
		for scanner.Scan() {
			ln++
			line := scanner.Text()
			if strings.Contains(line, term) {
				rel, _ := filepath.Rel(root, path)
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
