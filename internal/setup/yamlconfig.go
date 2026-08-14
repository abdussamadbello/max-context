package setup

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// mergeYAMLConfig registers max-context in a YAML MCP config (Hermes Agent's
// ~/.hermes/config.yaml) with the same guarantees as the JSON path: preserve
// what is there, stay idempotent, and refuse to touch a file we cannot parse.
//
// It works on yaml.Node rather than a map so the user's comments, key order,
// and formatting survive a merge. A global config is hand-maintained far more
// often than a per-project one, so silently reformatting it would be rude.
func mergeYAMLConfig(path, serversKey string, entry map[string]interface{}, r *Report) error {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}

	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist), err == nil && len(bytes.TrimSpace(raw)) == 0:
		doc := map[string]interface{}{serversKey: map[string]interface{}{serverName: entry}}
		out, mErr := marshalYAML(doc)
		if mErr != nil {
			return mErr
		}
		if wErr := os.WriteFile(path, out, 0644); wErr != nil {
			return wErr
		}
		r.created(path, "registered max-context")
		return nil
	case err != nil:
		return err
	}

	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		r.skipped(path, fmt.Sprintf("not valid YAML (%v); add max-context under %q yourself", err, serversKey))
		return nil
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		r.skipped(path, fmt.Sprintf("top level is not a mapping; add max-context under %q yourself", serversKey))
		return nil
	}
	top := root.Content[0]

	servers := findMapValue(top, serversKey)
	if servers == nil {
		servers = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		top.Content = append(top.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: serversKey},
			servers)
	}
	if servers.Kind != yaml.MappingNode {
		r.skipped(path, fmt.Sprintf("%q is not a mapping; add max-context there yourself", serversKey))
		return nil
	}
	if findMapValue(servers, serverName) != nil {
		r.unchanged(path, "max-context already registered")
		return nil
	}

	var entryNode yaml.Node
	if err := entryNode.Encode(entry); err != nil {
		return err
	}
	others := mapKeys(servers)
	servers.Content = append(servers.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: serverName},
		&entryNode)

	out, err := marshalYAML(&root)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		return err
	}

	note := "added max-context"
	if len(others) > 0 {
		note = fmt.Sprintf("added max-context, kept %d existing: %v", len(others), others)
	}
	r.updated(path, note)
	return nil
}

// findMapValue returns the value node for key in a mapping node, or nil.
func findMapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mapKeys lists the keys of a mapping node, in document order.
func mapKeys(m *yaml.Node) []string {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	out := make([]string, 0, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		out = append(out, m.Content[i].Value)
	}
	return out
}

// marshalYAML renders v with a consistent 2-space indent, so a file we create
// and a file we merge into come out looking the same.
func marshalYAML(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
