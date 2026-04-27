package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "siren.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadValid(t *testing.T) {
	p := writeTemp(t, `
operator:
  discord_user_id: "1"
discord:
  token_env: SIREN_TOKEN
services:
  - name: bots
    log_paths: ["/tmp/x.log"]
    log_match:
      regex: '(?i)error'
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Discord.OutboundQueueSize == 0 {
		t.Errorf("default queue size not applied")
	}
	if c.StateDir == "" {
		t.Errorf("default state dir not applied")
	}
}

func TestLoadMissingOperator(t *testing.T) {
	p := writeTemp(t, `
discord:
  token_env: X
services:
  - name: bots
    log_paths: ["/tmp/x.log"]
`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadDuplicateService(t *testing.T) {
	p := writeTemp(t, `
operator:
  discord_user_id: "1"
discord:
  token_env: X
services:
  - name: bots
    log_paths: ["/a"]
  - name: bots
    log_paths: ["/b"]
`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestLoadServiceWithoutSource(t *testing.T) {
	p := writeTemp(t, `
operator:
  discord_user_id: "1"
discord:
  token_env: X
services:
  - name: bots
`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for service with no sources")
	}
}

func TestLoadJournalValid(t *testing.T) {
	p := writeTemp(t, `
operator:
  discord_user_id: "1"
discord:
  token_env: X
services:
  - name: bots
    journal:
      unit: bots.service
      priority: err
      regex: '(?i)\b(error|panic)\b'
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	pri, ok := c.Services[0].Journal.JournalPriority()
	if !ok || pri != 3 {
		t.Fatalf("expected priority=3 ok, got %d ok=%v", pri, ok)
	}
}

func TestLoadJournalDefaultPriority(t *testing.T) {
	p := writeTemp(t, `
operator:
  discord_user_id: "1"
discord:
  token_env: X
services:
  - name: bots
    journal:
      unit: bots.service
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	pri, ok := c.Services[0].Journal.JournalPriority()
	if !ok || pri != 3 {
		t.Fatalf("default priority should be 3 (err), got %d ok=%v", pri, ok)
	}
}

func TestLoadJournalBadPriority(t *testing.T) {
	p := writeTemp(t, `
operator:
  discord_user_id: "1"
discord:
  token_env: X
services:
  - name: bots
    journal:
      unit: bots.service
      priority: bogus
`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for bad journal.priority")
	}
}

func TestLoadJournalBadRegex(t *testing.T) {
	p := writeTemp(t, `
operator:
  discord_user_id: "1"
discord:
  token_env: X
services:
  - name: bots
    journal:
      unit: bots.service
      regex: '(?P<'
`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for bad journal.regex")
	}
}
