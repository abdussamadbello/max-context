package setup

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// mergeTOMLConfig registers max-context in a TOML MCP config (Codex CLI's
// .codex/config.toml) under a `[<serversKey>.max-context]` table.
//
// Unlike the JSON and YAML paths this does not re-serialise the document. TOML
// tables may be declared in any order, so a new table appended at the end is
// always valid — which means the user's comments, ordering, and formatting
// survive byte for byte. Only the existence check needs a parser.
func mergeTOMLConfig(path, serversKey string, entry map[string]interface{}, r *Report) error {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}

	section := renderTOMLSection(serversKey, entry)

	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist), err == nil && len(bytes.TrimSpace(raw)) == 0:
		if wErr := os.WriteFile(path, []byte(section), 0644); wErr != nil {
			return wErr
		}
		r.created(path, "registered max-context")
		return nil
	case err != nil:
		return err
	}

	var doc map[string]interface{}
	if _, err := toml.Decode(string(raw), &doc); err != nil {
		r.skipped(path, fmt.Sprintf("not valid TOML (%v); append this yourself:\n%s", err, section))
		return nil
	}

	existing, present := doc[serversKey]
	servers, ok := existing.(map[string]interface{})
	if present && !ok {
		r.skipped(path, fmt.Sprintf("%q is not a table; append this yourself:\n%s", serversKey, section))
		return nil
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

	// Append, preserving every existing byte.
	body := string(raw)
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	body += "\n" + section
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		return err
	}

	note := "added max-context"
	if len(others) > 0 {
		note = fmt.Sprintf("added max-context, kept %d existing: %v", len(others), others)
	}
	r.updated(path, note)
	return nil
}

// renderTOMLSection writes the server definition as a standalone TOML table.
func renderTOMLSection(serversKey string, entry map[string]interface{}) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s.%s]\n", serversKey, tomlKey(serverName))
	// Deterministic field order so the output is stable across runs.
	keys := make([]string, 0, len(entry))
	for k := range entry {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %s\n", tomlKey(k), tomlValue(entry[k]))
	}
	return b.String()
}

// tomlKey quotes a key only when TOML's bare-key rules require it.
func tomlKey(s string) string {
	if s == "" {
		return `""`
	}
	for _, r := range s {
		bare := r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' ||
			r >= '0' && r <= '9' || r == '_' || r == '-'
		if !bare {
			return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
		}
	}
	return s
}

// tomlValue renders the value types a server entry can hold.
func tomlValue(v interface{}) string {
	switch t := v.(type) {
	case string:
		return `"` + strings.ReplaceAll(t, `"`, `\"`) + `"`
	case bool:
		if t {
			return "true"
		}
		return "false"
	case []interface{}:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			parts = append(parts, tomlValue(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]interface{}:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s = %s", tomlKey(k), tomlValue(t[k])))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return fmt.Sprintf("%v", t)
	}
}
