// Package state persists siren's small amount of cross-restart state to a
// single JSON file (atomic write via temp + rename).
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// File is the on-disk schema. Keep additions backwards-compatible.
type File struct {
	Version int                  `json:"version"`
	Mutes   map[string]time.Time `json:"mutes,omitempty"` // service -> expiry
	Acked   map[string]time.Time `json:"acked,omitempty"` // fingerprint -> expiry
}

// Store is a thread-safe wrapper around a state file.
type Store struct {
	mu   sync.Mutex
	path string
	data File
}

// Open loads the state file at path, creating an empty one if it does not exist.
func Open(path string) (*Store, error) {
	s := &Store{path: path, data: File{Version: 1}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, err
	}
	return s, nil
}

// Save writes the current state atomically.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".siren.state.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, s.path)
}
