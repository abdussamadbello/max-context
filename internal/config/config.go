package config

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/maxcontext/max-context/pkg/treesitter"
)

// DefaultMaxFileSize caps indexed files when config.json sets no maxFileSize.
const DefaultMaxFileSize int64 = 1 << 20 // 1 MB

// DefaultWatchDebounceMs is the watcher debounce when config.json sets none.
const DefaultWatchDebounceMs = 500

// Flags holds CLI flag values.
type Flags struct {
	Index   bool
	Reindex bool
	Status  bool
	Watch   bool
	Version bool
	Verbose bool
	Project string
	DB      string
}

// Register adds flags to fs and binds them to f.
func (f *Flags) Register(fs *flag.FlagSet) {
	fs.BoolVar(&f.Index, "index", false, "Build full index and start file watcher")
	fs.BoolVar(&f.Reindex, "reindex", false, "Force full rebuild of index")
	fs.BoolVar(&f.Status, "status", false, "Report index health and stats")
	fs.BoolVar(&f.Watch, "watch", false, "Start only the file watcher")
	fs.BoolVar(&f.Version, "version", false, "Print version and exit")
	fs.BoolVar(&f.Verbose, "verbose", false, "Enable debug logging")
	fs.StringVar(&f.Project, "project", ".", "Project root directory")
	fs.StringVar(&f.DB, "db", "", "Database file path (default: .max-context/index.db)")
}

// Config is the merged configuration (flags + optional config file).
type Config struct {
	Flags
	ProjectRoot string
	DBPath      string
	Verbose     bool
	ConfigFile  *ConfigFile
}

// ConfigFile is the optional .max-context/config.json structure.
type ConfigFile struct {
	Languages       []string `json:"languages"`
	Include         []string `json:"include"`
	Exclude         []string `json:"exclude"`
	WatchDebounceMs int      `json:"watchDebounceMs"`
	MaxFileSize     int64    `json:"maxFileSize"`
}

// Load merges flags with optional .max-context/config.json and returns Config.
func Load(projectRoot string, f *Flags) (*Config, error) {
	absProject, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, err
	}
	dbPath := f.DB
	if dbPath == "" {
		dbPath = filepath.Join(absProject, ".max-context", "index.db")
	}
	cfg := &Config{
		Flags:       *f,
		ProjectRoot: absProject,
		DBPath:      dbPath,
		Verbose:     f.Verbose,
	}
	configPath := filepath.Join(absProject, ".max-context", "config.json")
	if data, err := os.ReadFile(configPath); err == nil {
		var cf ConfigFile
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cf); err != nil {
			return nil, fmt.Errorf("parse %s: %w", configPath, err)
		}
		if err := dec.Decode(&struct{}{}); err != io.EOF {
			return nil, fmt.Errorf("parse %s: trailing JSON content", configPath)
		}
		if err := validateConfigFile(&cf); err != nil {
			return nil, fmt.Errorf("validate %s: %w", configPath, err)
		}
		cfg.ConfigFile = &cf
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", configPath, err)
	}
	return cfg, nil
}

func validateConfigFile(cf *ConfigFile) error {
	if cf.WatchDebounceMs < 0 {
		return fmt.Errorf("watchDebounceMs must be non-negative")
	}
	if cf.MaxFileSize < 0 {
		return fmt.Errorf("maxFileSize must be non-negative")
	}
	for _, lang := range cf.Languages {
		if len(treesitter.ExtensionsForLang(lang)) == 0 {
			return fmt.Errorf("unsupported language %q", lang)
		}
	}
	for _, pattern := range cf.Include {
		if _, err := filepath.Match(filepath.ToSlash(strings.TrimSpace(pattern)), "probe/path.go"); err != nil {
			return fmt.Errorf("invalid include glob %q: %w", pattern, err)
		}
	}
	for _, pattern := range cf.Exclude {
		pattern = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(pattern)), "!")
		if strings.ContainsAny(pattern, "*?[") {
			if _, err := filepath.Match(pattern, "probe/path.go"); err != nil {
				return fmt.Errorf("invalid exclude glob %q: %w", pattern, err)
			}
		}
	}
	return nil
}

// EffectiveMaxFileSize returns the configured maxFileSize, or DefaultMaxFileSize.
func (c *Config) EffectiveMaxFileSize() int64 {
	if c.ConfigFile != nil && c.ConfigFile.MaxFileSize > 0 {
		return c.ConfigFile.MaxFileSize
	}
	return DefaultMaxFileSize
}

// EffectiveDebounceMs returns the configured watchDebounceMs, or DefaultWatchDebounceMs.
func (c *Config) EffectiveDebounceMs() int {
	if c.ConfigFile != nil && c.ConfigFile.WatchDebounceMs > 0 {
		return c.ConfigFile.WatchDebounceMs
	}
	return DefaultWatchDebounceMs
}

// LanguageExtensions maps the configured language names to file extensions.
// An empty/absent languages list returns nil, meaning all supported languages.
func (c *Config) LanguageExtensions() []string {
	if c.ConfigFile == nil || len(c.ConfigFile.Languages) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var exts []string
	for _, lang := range c.ConfigFile.Languages {
		for _, e := range treesitter.ExtensionsForLang(lang) {
			if !seen[e] {
				seen[e] = true
				exts = append(exts, e)
			}
		}
	}
	return exts
}

// IncludeGlobs returns the configured include patterns (nil = include everything).
func (c *Config) IncludeGlobs() []string {
	if c.ConfigFile == nil {
		return nil
	}
	return c.ConfigFile.Include
}

// ExcludeGlobs returns the configured exclude patterns.
func (c *Config) ExcludeGlobs() []string {
	if c.ConfigFile == nil {
		return nil
	}
	return c.ConfigFile.Exclude
}
