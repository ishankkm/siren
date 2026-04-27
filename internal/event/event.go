// Package event defines siren's internal event type and fingerprinting.
package event

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
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

// NormalizeSummary collapses volatile substrings so repeated occurrences of
// the same logical error produce the same fingerprint. The order matters:
// structured tokens (UUID, IPv6, IPv4, long opaque alphanumeric tokens) are
// collapsed first, then remaining long digit/hex runs are folded
// character-by-character.
//
// This exists because dedup is fingerprint-based and the operator only
// receives one DM per fingerprint per suppression window. High-cardinality
// substrings (request IDs, peer addresses, shard numbers) would otherwise
// create a new fingerprint per line and bypass dedup, spamming the operator.
func NormalizeSummary(s string) string {
	s = reUUID.ReplaceAllString(s, "<UUID>")
	s = reIPv6.ReplaceAllString(s, "<IP>")
	s = reIPv4.ReplaceAllString(s, "<IP>")
	s = reLongAlnum.ReplaceAllStringFunc(s, collapseIfHasDigit)
	return collapseDigitHexRuns(s)
}

// reUUID matches the canonical 8-4-4-4-12 hex form, case-insensitive.
var reUUID = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)

// reIPv4 matches dotted-quad IPv4 addresses. Loose on per-octet range — we
// don't need RFC-strict validation, only structural recognition for dedup.
var reIPv4 = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)

// reIPv6 matches IPv6 addresses in two shapes:
//  1. at least three ':'-separated hex groups (covers "fe80::1ff:fe23:..."),
//  2. a leading '::' followed by hex groups (covers "::1", "::ffff:1.2.3.4").
//
// Avoids matching innocuous "key:value" two-token strings.
var reIPv6 = regexp.MustCompile(`\b[0-9a-fA-F]{1,4}(?::[0-9a-fA-F]{0,4}){2,}\b|::[0-9a-fA-F]{1,4}(?::[0-9a-fA-F]{1,4})*`)

// reLongAlnum matches alphanumeric runs of length >= 16 — candidates for
// opaque tokens. The "must contain a digit" filter is applied in
// collapseIfHasDigit since RE2 has no lookahead. The digit requirement
// avoids eating long camelCase symbol names from stack traces.
var reLongAlnum = regexp.MustCompile(`\b[A-Za-z0-9]{16,}\b`)

func collapseIfHasDigit(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			return "<TOKEN>"
		}
	}
	return s
}

func collapseDigitHexRuns(s string) string {
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
