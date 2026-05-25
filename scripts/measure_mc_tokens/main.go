//go:build measure
// +build measure

// Usage:
//   go run -tags=measure ./scripts/measure_mc_tokens <questions.json> <repo-root>
//
// Spawns max-context as an MCP child process, drives one tools/call per question,
// counts cl100k_base tokens in the JSON-RPC response, and writes a tightened
// questions file alongside the input (suffixed .measured).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/maxcontext/max-context/internal/bench"
)

type question struct {
	ID               string   `json:"id"`
	Text             string   `json:"text"`
	Category         string   `json:"category"`
	Tool             string   `json:"mc_tool"`
	Terms            []string `json:"baseline_terms"`
	MCResponseTokens int      `json:"mc_response_tokens"`
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: measure_mc_tokens <questions.json> <repo-root>")
		os.Exit(2)
	}
	qPath, repo := os.Args[1], os.Args[2]
	body, err := os.ReadFile(qPath)
	must(err, "read questions")
	var questions []question
	must(json.Unmarshal(body, &questions), "parse questions")

	counter, err := bench.NewCounter()
	must(err, "tiktoken")

	bin, err := filepath.Abs("./bin/max-context")
	must(err, "resolve binary path")
	cmd := exec.Command(bin)
	cmd.Dir = repo
	stdin, err := cmd.StdinPipe()
	must(err, "stdin pipe")
	stdout, err := cmd.StdoutPipe()
	must(err, "stdout pipe")
	cmd.Stderr = os.Stderr
	must(cmd.Start(), "start max-context")
	defer cmd.Process.Kill()

	reader := bufio.NewReader(stdout)
	send := func(req map[string]interface{}) {
		b, _ := json.Marshal(req)
		_, _ = stdin.Write(append(b, '\n'))
	}
	readOne := func() string {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			must(err, "read")
		}
		return line
	}

	// initialize
	send(map[string]interface{}{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]interface{}{"protocolVersion": "2024-11-05", "capabilities": map[string]interface{}{}},
	})
	_ = readOne()

	for i, q := range questions {
		args := buildArgs(q)
		send(map[string]interface{}{
			"jsonrpc": "2.0", "id": i + 1, "method": "tools/call",
			"params": map[string]interface{}{"name": q.Tool, "arguments": args},
		})
		resp := readOne()
		questions[i].MCResponseTokens = counter.Count(resp)
		fmt.Fprintf(os.Stderr, "%s (%s): %d tokens\n", q.ID, q.Tool, questions[i].MCResponseTokens)
	}

	out, _ := json.MarshalIndent(questions, "", "  ")
	outPath := qPath + ".measured"
	must(os.WriteFile(outPath, out, 0644), "write")
	fmt.Println("wrote", outPath)
}

func buildArgs(q question) map[string]interface{} {
	switch q.Tool {
	case "query_codebase":
		if len(q.Terms) == 0 {
			return map[string]interface{}{"query": q.Text, "max_results": 5}
		}
		return map[string]interface{}{"query": q.Terms[0], "max_results": 5}
	case "get_call_chain":
		if len(q.Terms) == 0 {
			return map[string]interface{}{"function_name": q.Text, "direction": "both", "depth": 2}
		}
		return map[string]interface{}{"function_name": q.Terms[0], "direction": "both", "depth": 2}
	case "get_impact":
		// For impact, the first term is treated as a file path hint. If the term doesn't
		// look like a path, fall back to letting get_impact diff against HEAD.
		if len(q.Terms) > 0 && (q.Terms[0] == "" || containsAny(q.Terms[0], ".go", ".ts", ".js", ".py", "/")) {
			return map[string]interface{}{"files": []string{q.Terms[0]}, "depth": 2}
		}
		return map[string]interface{}{"depth": 2}
	}
	return map[string]interface{}{}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

func must(err error, ctx string) {
	if err != nil {
		fmt.Fprintln(os.Stderr, ctx+":", err)
		os.Exit(1)
	}
}
