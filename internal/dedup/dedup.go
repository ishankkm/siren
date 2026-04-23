// Package dedup implements per-fingerprint suppression and a global
// rate limit for outbound events.
package dedup

import (
	"sync"
	"time"

	"github.com/ishankkm/siren/internal/event"
)

// Decision is what the dedup stage decides to do with an event.
type Decision int

const (
	// DecisionSend means: forward this event to the notifier now.
	DecisionSend Decision = iota
	// DecisionSuppress means: drop this event; it is within the suppression window.
	DecisionSuppress
)

// Deduper holds in-memory dedup and rate-limit state.
type Deduper struct {
	mu                sync.Mutex
	suppressionWindow time.Duration
	seen              map[string]time.Time // fingerprint -> last-sent time
}

// New creates a Deduper with the given suppression window.
func New(suppressionWindow time.Duration) *Deduper {
	return &Deduper{
		suppressionWindow: suppressionWindow,
		seen:              make(map[string]time.Time),
	}
}

// Check returns the decision for an event. It records the timestamp on send.
//
// TODO: implement burst rollup (count suppressed events per fingerprint and
// emit a summary when the suppression window closes).
// TODO: enforce a global token-bucket rate limit (max DM/min).
func (d *Deduper) Check(e event.Event, now time.Time) Decision {
	d.mu.Lock()
	defer d.mu.Unlock()

	last, ok := d.seen[e.Fingerprint]
	if ok && now.Sub(last) < d.suppressionWindow {
		return DecisionSuppress
	}
	d.seen[e.Fingerprint] = now
	return DecisionSend
}
