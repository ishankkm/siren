package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMuteRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "siren.state.json")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.Mute("bots", now.Add(time.Hour))
	if !s.IsMuted("bots", now) {
		t.Fatal("expected muted")
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.IsMuted("bots", now) {
		t.Fatal("expected muted after reload")
	}
	if s2.IsMuted("bots", now.Add(2*time.Hour)) {
		t.Fatal("expected expired")
	}
}

func TestUnmute(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.Mute("x", time.Now().Add(time.Hour))
	s.Unmute("x")
	if s.IsMuted("x", time.Now()) {
		t.Fatal("expected unmuted")
	}
}
