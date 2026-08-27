package contextcompiler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLexicalEvidenceCoversUnparseableSource(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/auth/jwt.go", "```go\npackage auth\nfunc manager() { secret := os.Getenv(\"JWT_SECRET\"); _ = secret }\n```")
	write("cmd/gateway/main.go", "```go\npackage main\nfunc main() { server := &http.Server{ReadTimeout: 5 * time.Second}; _ = server }\n```")

	evidence, warnings, err := lexicalEvidence(context.Background(), root,
		"Perform a security audit of authentication secrets and server hardening. Locate the relevant code.", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	foundAuth, foundServer := false, false
	for _, item := range evidence {
		foundAuth = foundAuth || (item.File == "internal/auth/jwt.go" && strings.Contains(item.Content, "JWT_SECRET"))
		foundServer = foundServer || (item.File == "cmd/gateway/main.go" && strings.Contains(item.Content, "ReadTimeout"))
	}
	if !foundAuth || !foundServer {
		t.Fatalf("lexical evidence missing fenced source auth=%v server=%v: %+v", foundAuth, foundServer, evidence)
	}
}

func TestLexicalEvidenceHonorsIndexScope(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/server.go", "package src\nvar serverTimeout = 5\n")
	write("excluded/secret.go", "package excluded\nvar jwtSecret = \"do-not-return\"\n")
	write("ignored/auth.go", "package ignored\nvar authSecret = \"do-not-return\"\n")
	write(".gitignore", "ignored/\n")

	evidence, _, err := lexicalEvidence(context.Background(), root,
		"Review authentication secret and server timeout configuration.", Options{
			Include: []string{"src/*.go", "excluded/*.go", "ignored/*.go"},
			Exclude: []string{"excluded/"},
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) == 0 {
		t.Fatal("expected in-scope lexical evidence")
	}
	for _, item := range evidence {
		if item.File != "src/server.go" {
			t.Fatalf("out-of-scope evidence returned: %+v", item)
		}
		if strings.Contains(item.Content, "do-not-return") {
			t.Fatalf("excluded content returned: %+v", item)
		}
	}
}
