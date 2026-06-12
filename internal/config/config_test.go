package config

import (
	"os"
	"path/filepath"
	"testing"
)

func loadWithConfigJSON(t *testing.T, body string) *Config {
	t.Helper()
	dir := t.TempDir()
	if body != "" {
		mcDir := filepath.Join(dir, ".max-context")
		if err := os.MkdirAll(mcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(mcDir, "config.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := Load(dir, &Flags{})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestDefaultsWithoutConfigFile(t *testing.T) {
	cfg := loadWithConfigJSON(t, "")
	if cfg.ConfigFile != nil {
		t.Fatalf("expected nil ConfigFile, got %+v", cfg.ConfigFile)
	}
	if got := cfg.EffectiveMaxFileSize(); got != DefaultMaxFileSize {
		t.Errorf("EffectiveMaxFileSize = %d, want %d", got, DefaultMaxFileSize)
	}
	if got := cfg.EffectiveDebounceMs(); got != DefaultWatchDebounceMs {
		t.Errorf("EffectiveDebounceMs = %d, want %d", got, DefaultWatchDebounceMs)
	}
	if got := cfg.LanguageExtensions(); got != nil {
		t.Errorf("LanguageExtensions = %v, want nil", got)
	}
	if got := cfg.IncludeGlobs(); got != nil {
		t.Errorf("IncludeGlobs = %v, want nil", got)
	}
	if got := cfg.ExcludeGlobs(); got != nil {
		t.Errorf("ExcludeGlobs = %v, want nil", got)
	}
}

func TestConfigFileOverrides(t *testing.T) {
	cfg := loadWithConfigJSON(t, `{
		"languages": ["go", "TypeScript"],
		"include": ["src/*.go"],
		"exclude": ["generated/"],
		"watchDebounceMs": 250,
		"maxFileSize": 2048
	}`)
	if cfg.ConfigFile == nil {
		t.Fatal("ConfigFile not loaded")
	}
	if got := cfg.EffectiveMaxFileSize(); got != 2048 {
		t.Errorf("EffectiveMaxFileSize = %d, want 2048", got)
	}
	if got := cfg.EffectiveDebounceMs(); got != 250 {
		t.Errorf("EffectiveDebounceMs = %d, want 250", got)
	}
	exts := cfg.LanguageExtensions()
	want := map[string]bool{".go": true, ".ts": true, ".tsx": true}
	if len(exts) != len(want) {
		t.Fatalf("LanguageExtensions = %v, want keys %v", exts, want)
	}
	for _, e := range exts {
		if !want[e] {
			t.Errorf("unexpected extension %q in %v", e, exts)
		}
	}
	if got := cfg.IncludeGlobs(); len(got) != 1 || got[0] != "src/*.go" {
		t.Errorf("IncludeGlobs = %v", got)
	}
	if got := cfg.ExcludeGlobs(); len(got) != 1 || got[0] != "generated/" {
		t.Errorf("ExcludeGlobs = %v", got)
	}
}

func TestUnknownLanguageIgnored(t *testing.T) {
	cfg := loadWithConfigJSON(t, `{"languages": ["brainfuck", "py"]}`)
	exts := cfg.LanguageExtensions()
	want := map[string]bool{".py": true, ".pyi": true}
	if len(exts) != len(want) {
		t.Fatalf("LanguageExtensions = %v, want keys %v", exts, want)
	}
	for _, e := range exts {
		if !want[e] {
			t.Errorf("unexpected extension %q", e)
		}
	}
}
