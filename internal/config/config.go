// Package config loads and validates siren's YAML configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level siren configuration.
type Config struct {
	Operator Operator  `yaml:"operator"`
	Discord  Discord   `yaml:"discord"`
	Services []Service `yaml:"services"`
	Dedup    Dedup     `yaml:"dedup"`
	Redact   []Redact  `yaml:"redact"`
	StateDir string    `yaml:"state_dir"`
}

// Operator identifies the single Discord user that receives DMs.
type Operator struct {
	DiscordUserID string `yaml:"discord_user_id"`
}

// Discord holds Discord-client settings. The bot token itself is read from
// the environment variable named in TokenEnv, never from the config file.
type Discord struct {
	TokenEnv          string `yaml:"token_env"`
	OutboundQueueSize int    `yaml:"outbound_queue_size"`
}

// Service describes a single monitored service.
type Service struct {
	Name      string   `yaml:"name"`
	LogPaths  []string `yaml:"log_paths"`
	LogMatch  LogMatch `yaml:"log_match"`
	LogLevels []string `yaml:"log_levels"`
	Process   Process  `yaml:"process"`
	Health    Health   `yaml:"health"`
}

// LogMatch configures plain-text log matching.
type LogMatch struct {
	Regex string `yaml:"regex"`
}

// Process configures process / unit watching.
type Process struct {
	SystemdUnit string `yaml:"systemd_unit"`
	PIDFile     string `yaml:"pid_file"`
}

// Health configures an HTTP or TCP health probe.
type Health struct {
	URL      string        `yaml:"url"`
	Interval time.Duration `yaml:"interval"`
}

// Dedup configures the suppression / rate-limit stage.
type Dedup struct {
	SuppressionWindow time.Duration `yaml:"suppression_window"`
	MaxDMPerMinute    int           `yaml:"max_dm_per_minute"`
}

// Redact is a single regex redaction rule.
type Redact struct {
	Pattern     string `yaml:"pattern"`
	Replacement string `yaml:"replacement"`
}

// Load reads, parses, and validates a YAML config file from disk.
// Defaults are applied for omitted optional fields.
func Load(path string) (*Config, error) {
	if path == "" {
		return nil, errors.New("config path is empty")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// applyDefaults fills in safe defaults for optional fields.
func (c *Config) applyDefaults() {
	if c.Discord.OutboundQueueSize == 0 {
		c.Discord.OutboundQueueSize = 256
	}
	if c.Dedup.SuppressionWindow == 0 {
		c.Dedup.SuppressionWindow = 5 * time.Minute
	}
	if c.Dedup.MaxDMPerMinute == 0 {
		c.Dedup.MaxDMPerMinute = 10
	}
	if c.StateDir == "" {
		c.StateDir = "/var/lib/siren"
	}
	for i := range c.Services {
		if c.Services[i].Health.URL != "" && c.Services[i].Health.Interval == 0 {
			c.Services[i].Health.Interval = 15 * time.Second
		}
	}
}

// Validate returns an error describing the first problem found.
func (c *Config) Validate() error {
	if c.Operator.DiscordUserID == "" {
		return errors.New("operator.discord_user_id is required")
	}
	if c.Discord.TokenEnv == "" {
		return errors.New("discord.token_env is required")
	}
	if len(c.Services) == 0 {
		return errors.New("at least one service must be configured")
	}
	seen := make(map[string]struct{})
	for i, s := range c.Services {
		if s.Name == "" {
			return fmt.Errorf("services[%d].name is required", i)
		}
		if _, dup := seen[s.Name]; dup {
			return fmt.Errorf("duplicate service name %q", s.Name)
		}
		seen[s.Name] = struct{}{}
		if s.LogMatch.Regex != "" {
			if _, err := regexp.Compile(s.LogMatch.Regex); err != nil {
				return fmt.Errorf("services[%d] (%s): bad log_match.regex: %w", i, s.Name, err)
			}
		}
		hasSource := len(s.LogPaths) > 0 || s.Process.SystemdUnit != "" || s.Process.PIDFile != "" || s.Health.URL != ""
		if !hasSource {
			return fmt.Errorf("services[%d] (%s): no log_paths, process, or health configured", i, s.Name)
		}
	}
	for i, r := range c.Redact {
		if r.Pattern == "" {
			return fmt.Errorf("redact[%d]: pattern is required", i)
		}
		if _, err := regexp.Compile(r.Pattern); err != nil {
			return fmt.Errorf("redact[%d]: bad pattern: %w", i, err)
		}
	}
	return nil
}

// Token returns the Discord bot token from the configured environment variable.
func (c *Config) Token() (string, error) {
	v := os.Getenv(c.Discord.TokenEnv)
	if v == "" {
		return "", fmt.Errorf("env var %s is unset", c.Discord.TokenEnv)
	}
	return v, nil
}
