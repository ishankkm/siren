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

// Open loads the state file at path, creating an empty in-memory state if it
// does not exist. The parent directory must already exist.
func Open(path string) (*Store, error) {
	s := &Store{path: path, data: File{Version: 1, Mutes: map[string]time.Time{}, Acked: map[string]time.Time{}}}
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
	if s.data.Mutes == nil {
		s.data.Mutes = map[string]time.Time{}
	}
	if s.data.Acked == nil {
		s.data.Acked = map[string]time.Time{}
	}
	return s, nil
}

// Mute records an active mute for a service.
func (s *Store) Mute(service string, until time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Mutes[service] = until
}

// Unmute removes a mute.
func (s *Store) Unmute(service string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Mutes, service)
}

// IsMuted reports whether a service is currently muted (and prunes expired entries).
func (s *Store) IsMuted(service string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.data.Mutes[service]
	if !ok {
		return false
	}
	if !now.Before(exp) {
		delete(s.data.Mutes, service)
		return false
	}
	return true
}

// Ack records an acknowledgement for a fingerprint with a TTL.
func (s *Store) Ack(fingerprint string, until time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Acked[fingerprint] = until
}

// Mutes returns a copy of the current mute map.
func (s *Store) Mutes() map[string]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]time.Time, len(s.data.Mutes))
	for k, v := range s.data.Mutes {
		out[k] = v
	}
	return out
}

// Save writes the current state atomically.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".siren.state.*.tmp")
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
