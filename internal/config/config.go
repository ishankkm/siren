// Package config loads and validates siren's YAML configuration.
//
// The full YAML parser is intentionally not wired up in this scaffold; the
// real implementation will use gopkg.in/yaml.v3. For now Load returns a
// zero-value Config so the rest of the program can be built and run.
package config

import (
	"errors"
	"os"
	"time"
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

// Load reads and parses a YAML config file from disk.
//
// TODO: parse YAML with gopkg.in/yaml.v3 and validate required fields.
func Load(path string) (*Config, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	if path == "" {
		return nil, errors.New("config path is empty")
	}
	return &Config{}, nil
}
