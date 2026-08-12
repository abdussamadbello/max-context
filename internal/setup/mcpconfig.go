package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// serverName is the key max-context registers itself under in an IDE's MCP config.
const serverName = "max-context"

// Action describes what setup did to one file, for the summary printed to the user.
type Action string

const (
	ActionCreated   Action = "created"
	ActionUpdated   Action = "updated"
	ActionUnchanged Action = "unchanged"
	ActionSkipped   Action = "skipped"
)

// Change is a single file outcome.
type Change struct {
	Action Action
	Path   string
	Note   string
}

// Report collects what setup touched so the command can tell the user what
// happened instead of exiting silently. Every method is nil-safe, so callers
// that do not want a report (tests, "all" sub-runs) can pass nil.
type Report struct {
	Changes []Change
	// Notes are steps max-context cannot take for the user — a harness that
	// needs the project marked trusted, for instance. Printed after the summary.
	Notes []string
	root  string
}

func NewReport(root string) *Report { return &Report{root: root} }

func (r *Report) add(a Action, path, note string) {
	if r == nil {
		return
	}
	// Relative inside the project, absolute outside it: a harness with only a
	// global config (Hermes) writes outside the repo, and "../../.hermes/..."
	// would hide that.
	if rel, err := filepath.Rel(r.root, path); err == nil && rel != "" && !strings.HasPrefix(rel, "..") {
		path = rel
	}
	r.Changes = append(r.Changes, Change{Action: a, Path: path, Note: note})
}

func (r *Report) created(path, note string)   { r.add(ActionCreated, path, note) }
func (r *Report) updated(path, note string)   { r.add(ActionUpdated, path, note) }
func (r *Report) unchanged(path, note string) { r.add(ActionUnchanged, path, note) }
func (r *Report) skipped(path, note string)   { r.add(ActionSkipped, path, note) }

// note records a follow-up step for the user, once.
func (r *Report) note(text string) {
	if r == nil || text == "" {
		return
	}
	for _, existing := range r.Notes {
		if existing == text {
			return
		}
	}
	r.Notes = append(r.Notes, text)
}

// Skipped reports whether any file needs the user's attention.
func (r *Report) Skipped() []Change {
	if r == nil {
		return nil
	}
	var out []Change
	for _, c := range r.Changes {
		if c.Action == ActionSkipped {
			out = append(out, c)
		}
	}
	return out
}

// String renders the summary: written files first, then anything skipped.
func (r *Report) String() string {
	if r == nil || len(r.Changes) == 0 {
		return "Nothing to do.\n"
	}
	var b bytes.Buffer
	for _, c := range r.Changes {
		if c.Action == ActionSkipped {
			continue
		}
		fmt.Fprintf(&b, "  %-9s %s", c.Action, c.Path)
		if c.Note != "" {
			fmt.Fprintf(&b, "  (%s)", c.Note)
		}
		b.WriteByte('\n')
	}
	for _, c := range r.Skipped() {
		fmt.Fprintf(&b, "  SKIPPED   %s\n            %s\n", c.Path, c.Note)
	}
	return b.String()
}

// mcpServerEntry is the server definition max-context writes into IDE configs.
func mcpServerEntry() map[string]interface{} {
	return map[string]interface{}{
		"command": serverName,
		"args":    []interface{}{},
	}
}

// manualSnippet is what we tell the user to paste when we refuse to edit a file.
func manualSnippet(serversKey string) string {
	doc := map[string]interface{}{serversKey: map[string]interface{}{serverName: mcpServerEntry()}}
	out, _ := json.Marshal(doc)
	return string(out)
}

// mergeMCPConfig registers max-context in the MCP config at path while preserving
// every server and top-level key already there.
//
// This deliberately does NOT use write-if-not-exists: any user who has ever
// configured another MCP server already has this file, and skipping it left
// max-context unregistered while setup reported success. Behaviour:
//
//   - file absent or empty  -> create it with just max-context
//   - max-context present   -> leave it alone (idempotent)
//   - other servers present -> add max-context alongside them
//   - unparseable JSON      -> refuse to touch it and tell the user what to paste
//
// A file we cannot parse is never overwritten: hand-written config (including
// JSONC with comments) is worth more than an automatic edit.
func mergeMCPConfig(path, serversKey string, r *Report) error {
	return mergeMCPConfigEntry(path, serversKey, mcpServerEntry(), r)
}

// mergeMCPConfigEntry is mergeMCPConfig with the server definition supplied by
// the caller. Harnesses disagree on the shape of a server entry — opencode
// wants {"type":"local","command":["max-context"]} where most want
// {"command":"max-context","args":[]} — so the shape travels with the harness.
func mergeMCPConfigEntry(path, serversKey string, entry map[string]interface{}, r *Report) error {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}

	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist), err == nil && len(bytes.TrimSpace(raw)) == 0:
		doc := map[string]interface{}{serversKey: map[string]interface{}{serverName: entry}}
		if err := writeJSONFile(path, doc); err != nil {
			return err
		}
		r.created(path, "registered max-context")
		return nil
	case err != nil:
		return err
	}

	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		r.skipped(path, fmt.Sprintf("not valid JSON (%v); add this yourself: %s", err, manualSnippet(serversKey)))
		return nil
	}

	existing, present := doc[serversKey]
	servers, ok := existing.(map[string]interface{})
	if present && !ok {
		r.skipped(path, fmt.Sprintf("%q is not an object; add this yourself: %s", serversKey, manualSnippet(serversKey)))
		return nil
	}
	if servers == nil {
		servers = map[string]interface{}{}
	}
	if _, already := servers[serverName]; already {
		r.unchanged(path, "max-context already registered")
		return nil
	}

	others := make([]string, 0, len(servers))
	for name := range servers {
		others = append(others, name)
	}
	sort.Strings(others)

	servers[serverName] = entry
	doc[serversKey] = servers
	if err := writeJSONFile(path, doc); err != nil {
		return err
	}

	note := "added max-context"
	if len(others) > 0 {
		note = fmt.Sprintf("added max-context, kept %d existing: %v", len(others), others)
	}
	r.updated(path, note)
	return nil
}

// writeJSONFile writes v as indented JSON with a trailing newline.
func writeJSONFile(path string, v interface{}) error {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0644)
}

// writeFileIfAbsent writes content only when path does not already exist, and
// records the outcome. Correct for files max-context owns outright (skills,
// rules, command docs) — unlike MCP configs, there is nothing to merge into.
func writeFileIfAbsent(path, content string, perm os.FileMode, r *Report) error {
	if _, err := os.Stat(path); err == nil {
		r.unchanged(path, "")
		return nil
	}
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		return err
	}
	r.created(path, "")
	return nil
}

// addInstructionsPath registers the guidance file in a config key holding an
// array of instruction paths. opencode has no AGENTS.md convention; it loads
// whatever its `instructions` array points at, so the guidance file is inert
// until it is listed there.
func addInstructionsPath(path, key, guidance string, r *Report) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err // the config was just written by mergeMCPConfigEntry
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		r.skipped(path, fmt.Sprintf("not valid JSON; add %q to %q yourself", guidance, key))
		return nil
	}

	existing, present := doc[key]
	list, ok := existing.([]interface{})
	if present && !ok {
		r.skipped(path, fmt.Sprintf("%q is not an array; add %q to it yourself", key, guidance))
		return nil
	}
	want := filepath.ToSlash(guidance)
	for _, v := range list {
		if s, ok := v.(string); ok && filepath.ToSlash(s) == want {
			return nil // already listed
		}
	}
	doc[key] = append(list, want)
	if err := writeJSONFile(path, doc); err != nil {
		return err
	}
	r.updated(path, fmt.Sprintf("listed %s under %q", want, key))
	return nil
}
