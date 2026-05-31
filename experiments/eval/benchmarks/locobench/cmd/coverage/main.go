// Command coverage statically measures, across every LoCoBench scenario, how
// much of each task's required context max-context could even index — with NO
// LLM calls. It quantifies the two coverage gaps the first run surfaced:
//
//  1. unsupported code languages (C#, PHP — no tree-sitter binding here), and
//  2. non-code artifacts (.md/.yaml/.json/.proto/… — never indexed at all).
//
// A scenario is "fully indexable" only if every one of its context_files has an
// extension max-context can parse. Run:
//
//	go run ./cmd/coverage --data data
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maxcontext/eval-locobench/internal/scenario"
)

// indexable mirrors pkg/treesitter/bindings.go LanguageForExt (the ground truth
// for what the indexer will parse). Keep in sync if the binding set changes.
var indexable = map[string]bool{
	"ts": true, "tsx": true, "js": true, "jsx": true, "mjs": true, "cjs": true,
	"py": true, "pyi": true, "go": true, "rs": true, "java": true,
	"c": true, "h": true, "cc": true, "cpp": true, "cxx": true,
	"hh": true, "hpp": true, "hxx": true, "rb": true, "swift": true,
}

// unsupportedLangPrefix: LoCoBench primary languages with no binding here.
var unsupportedLang = map[string]bool{"csharp": true, "php": true}

func ext(p string) string {
	base := filepath.Base(p)
	if i := strings.LastIndex(base, "."); i >= 0 {
		return strings.ToLower(base[i+1:])
	}
	return "(noext)"
}

func main() {
	dataDir := flag.String("data", "data", "LoCoBench data root (holds output/scenarios)")
	listOut := flag.String("list-fully-indexable", "", "if set, write the fully-indexable scenario ids to this file")
	flag.Parse()

	scns, err := scenario.LoadDir(filepath.Join(*dataDir, "output", "scenarios"), nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	total := len(scns)
	var fullyIndexable []string
	langTotal := map[string]int{}
	artifactExt := map[string]int{}        // non-code missing exts (supported-lang scenarios only)
	supTotal, supWithArtifact := 0, 0
	catTotal := map[string]int{}
	catAffected := map[string]int{}

	for _, s := range scns {
		prefix, _ := s.ProjectPrefix()
		lang := strings.SplitN(prefix, "_", 2)[0]
		langTotal[lang]++
		catTotal[s.TaskCategory]++

		allIdx := true
		hasArtifact := false
		for _, cf := range s.ContextFiles {
			if !indexable[ext(cf)] {
				allIdx = false
				if !unsupportedLang[lang] {
					artifactExt[ext(cf)]++
					hasArtifact = true
				}
			}
		}
		if allIdx {
			fullyIndexable = append(fullyIndexable, s.ID)
		} else {
			catAffected[s.TaskCategory]++
		}
		if !unsupportedLang[lang] {
			supTotal++
			if hasArtifact {
				supWithArtifact++
			}
		}
	}

	fmt.Printf("=== %d scenarios ===\n", total)
	fmt.Printf("fully indexable (every context file is parseable code): %d (%.1f%%)\n\n",
		len(fullyIndexable), pct(len(fullyIndexable), total))

	fmt.Println("=== primary language support ===")
	for _, l := range sortedKeys(langTotal) {
		tag := "supported"
		if unsupportedLang[l] {
			tag = "UNSUPPORTED (no tree-sitter binding)"
		}
		fmt.Printf("  %-12s %4d  %s\n", l, langTotal[l], tag)
	}

	fmt.Printf("\n=== among %d supported-language scenarios ===\n", supTotal)
	fmt.Printf("require >=1 non-code artifact MC can't index: %d (%.1f%%)\n", supWithArtifact, pct(supWithArtifact, supTotal))
	fmt.Println("top missing artifact types:")
	for _, e := range topN(artifactExt, 12) {
		fmt.Printf("  .%-8s %d\n", e.k, e.v)
	}

	fmt.Println("\n=== scenarios touching an unindexable file, by category ===")
	for _, c := range sortedKeys(catTotal) {
		fmt.Printf("  %-28s %4d/%-4d (%.0f%%)\n", c, catAffected[c], catTotal[c], pct(catAffected[c], catTotal[c]))
	}

	if *listOut != "" {
		sort.Strings(fullyIndexable)
		if err := os.WriteFile(*listOut, []byte(strings.Join(fullyIndexable, "\n")+"\n"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "write list:", err)
			os.Exit(1)
		}
		fmt.Printf("\nwrote %d fully-indexable scenario ids to %s\n", len(fullyIndexable), *listOut)
	}
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

func sortedKeys(m map[string]int) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

type kv struct {
	k string
	v int
}

func topN(m map[string]int, n int) []kv {
	kvs := make([]kv, 0, len(m))
	for k, v := range m {
		kvs = append(kvs, kv{k, v})
	}
	sort.Slice(kvs, func(i, j int) bool { return kvs[i].v > kvs[j].v })
	if len(kvs) > n {
		kvs = kvs[:n]
	}
	return kvs
}
