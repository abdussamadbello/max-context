package artifacts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Status struct {
	Healthy              bool      `json:"healthy"`
	LastFullIndex        time.Time `json:"lastFullIndex"`
	LastIncrementalIndex time.Time `json:"lastIncrementalIndex"`
	TotalFunctions       int       `json:"totalFunctions"`
	TotalFiles           int       `json:"totalFiles"`
	DirtyFiles           []string  `json:"dirtyFiles"`
	ReindexInProgress    bool      `json:"reindexInProgress"`
	Version              string    `json:"version"`
}

func WriteStatus(dir string, s *Status) error {
	os.MkdirAll(dir, 0755)
	p := filepath.Join(dir, "status.json")
	tmp := p + ".tmp"
	data, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(tmp, data, 0644)
	return os.Rename(tmp, p)
}

func ReadStatus(dir string) (*Status, error) {
	data, err := os.ReadFile(filepath.Join(dir, "status.json"))
	if err != nil {
		return nil, err
	}
	var s Status
	err = json.Unmarshal(data, &s)
	return &s, err
}
