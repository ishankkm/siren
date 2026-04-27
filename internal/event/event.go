// Package event defines siren's internal event type and fingerprinting.
package event

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// Severity classifies an event.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarn
	SeverityError
	SeverityCritical
)

// String returns a lowercase name for the severity.
func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarn:
		return "warn"
	case SeverityError:
		return "error"
	case SeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// ParseSeverity maps common log-level strings to a Severity.
// Unknown values map to SeverityError (the safe default for an alerting tool).
func ParseSeverity(s string) Severity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace", "debug", "info", "informational", "notice":
		return SeverityInfo
	case "warn", "warning":
		return SeverityWarn
	case "err", "error":
		return SeverityError
	case "fatal", "crit", "critical", "emerg", "emergency", "panic", "alert":
		return SeverityCritical
	default:
		return SeverityError
	}
}

// Source identifies which collector produced an event.
type Source string

const (
	SourceLog     Source = "log"
	SourceJournal Source = "journal"
	SourceProcess Source = "process"
	SourceProbe   Source = "probe"
)

// Event is the normalized representation of any signal flowing through siren.
type Event struct {
	Service     string
	Source      Source
	Severity    Severity
	Summary     string
	Detail      string
	Timestamp   time.Time
	Fingerprint string
}

// Fingerprint produces a stable hash from (service, source, summary).
// The summary is expected to already be normalized (digits/UUIDs collapsed)
// by the collector before fingerprinting.
func Fingerprint(service string, source Source, normalizedSummary string) string {
	h := sha256.New()
	h.Write([]byte(service))
	h.Write([]byte{0})
	h.Write([]byte(source))
	h.Write([]byte{0})
	h.Write([]byte(normalizedSummary))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// NormalizeSummary collapses volatile substrings (long digit and hex runs)
// so repeated occurrences of the same logical error produce the same
// fingerprint.
func NormalizeSummary(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	rs := []rune(s)
	i := 0
	for i < len(rs) {
		r := rs[i]
		if r >= '0' && r <= '9' {
			j := i
			for j < len(rs) && rs[j] >= '0' && rs[j] <= '9' {
				j++
			}
			if j-i >= 3 {
				b.WriteString("<N>")
			} else {
				b.WriteString(string(rs[i:j]))
			}
			i = j
			continue
		}
		if isHex(r) {
			j := i
			for j < len(rs) && isHex(rs[j]) {
				j++
			}
			if j-i >= 8 {
				b.WriteString("<HEX>")
			} else {
				b.WriteString(string(rs[i:j]))
			}
			i = j
			continue
		}
		b.WriteRune(r)
		i++
	}
	return b.String()
}

func isHex(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}
