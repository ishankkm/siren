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
	// DecisionRateLimited means: dropped because the global rate limit is exhausted.
	DecisionRateLimited
)

// Deduper holds in-memory dedup and rate-limit state.
type Deduper struct {
	mu                sync.Mutex
	suppressionWindow time.Duration
	maxPerMinute      int

	seen map[string]time.Time // fingerprint -> last-sent

	// token bucket for global rate limit
	tokens   float64
	lastFill time.Time
}

// New creates a Deduper with the given suppression window and rate limit.
// A maxPerMinute of <= 0 disables the rate limit. The token bucket is
// initialized full and its clock baseline is set on the first Check call,
// so callers may pass synthetic timestamps for testing.
func New(suppressionWindow time.Duration, maxPerMinute int) *Deduper {
	return &Deduper{
		suppressionWindow: suppressionWindow,
		maxPerMinute:      maxPerMinute,
		seen:              make(map[string]time.Time),
		tokens:            float64(maxPerMinute),
		// lastFill left zero; lazily set on first Check.
	}
}

// Check returns the decision for an event. On DecisionSend it records the
// timestamp and consumes a rate-limit token.
func (d *Deduper) Check(e event.Event, now time.Time) Decision {
	d.mu.Lock()
	defer d.mu.Unlock()

	if last, ok := d.seen[e.Fingerprint]; ok && now.Sub(last) < d.suppressionWindow {
		return DecisionSuppress
	}

	if d.maxPerMinute > 0 {
		if d.lastFill.IsZero() {
			d.lastFill = now
		}
		// Refill at maxPerMinute / 60 tokens per second.
		elapsed := now.Sub(d.lastFill).Seconds()
		if elapsed > 0 {
			d.tokens += elapsed * float64(d.maxPerMinute) / 60.0
			if d.tokens > float64(d.maxPerMinute) {
				d.tokens = float64(d.maxPerMinute)
			}
			d.lastFill = now
		}
		if d.tokens < 1 {
			return DecisionRateLimited
		}
		d.tokens--
	}

	d.seen[e.Fingerprint] = now
	return DecisionSend
}

// Forget clears any suppression state for a fingerprint, e.g. after a manual
// !ack so a later occurrence is delivered immediately.
func (d *Deduper) Forget(fingerprint string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.seen, fingerprint)
}
