package dedup

import (
	"testing"
	"time"

	"github.com/ishankkm/siren/internal/event"
)

func ev(fp string) event.Event { return event.Event{Fingerprint: fp} }

func TestSuppressionWindow(t *testing.T) {
	d := New(5*time.Minute, 0)
	now := time.Unix(1000, 0)
	if got := d.Check(ev("a"), now); got != DecisionSend {
		t.Fatalf("first: got %v", got)
	}
	if got := d.Check(ev("a"), now.Add(time.Minute)); got != DecisionSuppress {
		t.Fatalf("within window: got %v", got)
	}
	if got := d.Check(ev("a"), now.Add(6*time.Minute)); got != DecisionSend {
		t.Fatalf("after window: got %v", got)
	}
}

func TestRateLimit(t *testing.T) {
	d := New(time.Hour, 2) // 2 per minute
	now := time.Unix(2000, 0)
	if got := d.Check(ev("a"), now); got != DecisionSend {
		t.Fatalf("a: %v", got)
	}
	if got := d.Check(ev("b"), now); got != DecisionSend {
		t.Fatalf("b: %v", got)
	}
	if got := d.Check(ev("c"), now); got != DecisionRateLimited {
		t.Fatalf("c: expected rate-limit, got %v", got)
	}
	// After enough time, tokens refill.
	if got := d.Check(ev("c"), now.Add(time.Minute)); got != DecisionSend {
		t.Fatalf("c after refill: %v", got)
	}
}

func TestForget(t *testing.T) {
	d := New(time.Hour, 0)
	now := time.Unix(3000, 0)
	d.Check(ev("a"), now)
	if got := d.Check(ev("a"), now.Add(time.Minute)); got != DecisionSuppress {
		t.Fatalf("expected suppress, got %v", got)
	}
	d.Forget("a")
	if got := d.Check(ev("a"), now.Add(2*time.Minute)); got != DecisionSend {
		t.Fatalf("after forget: %v", got)
	}
}
