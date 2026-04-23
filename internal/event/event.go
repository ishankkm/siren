// Package event defines siren's internal event type and fingerprinting.
package event

import (
	"crypto/sha256"
	"encoding/hex"
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

// Source identifies which collector produced an event.
type Source string

const (
	SourceLog     Source = "log"
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
