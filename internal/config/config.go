package config

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
)

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
	ProjectRoot    string
	DBPath         string
	Verbose        bool
	ConfigFile     *ConfigFile
}

// ConfigFile is the optional .max-context/config.json structure.
type ConfigFile struct {
	Languages      []string `json:"languages"`
	Include        []string `json:"include"`
	Exclude        []string `json:"exclude"`
	WatchDebounceMs int     `json:"watchDebounceMs"`
	MaxFileSize    int64    `json:"maxFileSize"`
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
		if json.Unmarshal(data, &cf) == nil {
			cfg.ConfigFile = &cf
		}
	}
	return cfg, nil
}
