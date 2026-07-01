package faceai

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// storageSchema is the on-disk representation.
type storageSchema struct {
	Version    int                          `json:"version"`
	Creators   map[CreatorID]creator      `json:"creators"`
	Embeddings map[CreatorID][][]float64  `json:"embeddings"`
}

type creator struct {
	Name string `json:"name"`
}

func (c *storageSchema) ensure() {
	if c.Version == 0 {
		c.Version = 1
	}
	if c.Creators == nil {
		c.Creators = map[CreatorID]creator{}
	}
	if c.Embeddings == nil {
		c.Embeddings = map[CreatorID][][]float64{}
	}
}

func defaultDataDir() string { return "./data" }

func ensureDir(dir string) error {
	if dir == "" {
		return errors.New("data dir is empty")
	}
	return os.MkdirAll(dir, 0o755)
}

func (s *storageSchema) pathFor(dataDir string) string {
	return filepath.Join(dataDir, "faceai_creators.json")
}

func loadFromDisk(dataDir string) (*storageSchema, error) {
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	p := (&storageSchema{}).pathFor(dataDir)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var out storageSchema
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	out.ensure()
	return &out, nil
}

func saveToDisk(dataDir string, st *storageSchema) error {
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	if err := ensureDir(dataDir); err != nil {
		return err
	}
	p := (&storageSchema{}).pathFor(dataDir)

	st.ensure()
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}
