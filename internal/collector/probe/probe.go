// Package probe implements the HTTP/TCP health-probe collector.
package probe

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/ishankkm/siren/internal/event"
)

// Collector polls a single endpoint at a fixed interval and emits an event
// on each up<->down transition.
type Collector struct {
	service  string
	target   string
	interval time.Duration
	logger   *slog.Logger
	http     *http.Client
}

// New constructs a probe collector. interval defaults to 15s if zero.
func New(service, target string, interval time.Duration, logger *slog.Logger) *Collector {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	return &Collector{
		service:  service,
		target:   target,
		interval: interval,
		logger:   logger.With("service", service, "collector", "probe", "target", target),
		http:     &http.Client{Timeout: 5 * time.Second},
	}
}

// Name implements collector.Collector.
func (c *Collector) Name() string {
	return fmt.Sprintf("probe:%s", c.service)
}

// Run polls until ctx is cancelled.
func (c *Collector) Run(ctx context.Context, out chan<- event.Event) error {
	t := time.NewTicker(c.interval)
	defer t.Stop()

	var prevUp = true // assume healthy until first failure
	var firstCheck = true

	check := func() {
		up, detail := c.checkOnce(ctx)
		if firstCheck {
			firstCheck = false
			prevUp = up
			if !up {
				c.emit(out, false, detail)
			}
			return
		}
		if up == prevUp {
			return
		}
		prevUp = up
		c.emit(out, up, detail)
	}

	check()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			check()
		}
	}
}

func (c *Collector) emit(out chan<- event.Event, up bool, detail string) {
	sev := event.SeverityCritical
	summary := fmt.Sprintf("%s probe DOWN", c.target)
	if up {
		sev = event.SeverityInfo
		summary = fmt.Sprintf("%s probe recovered", c.target)
	}
	ev := event.Event{
		Service:     c.service,
		Source:      event.SourceProbe,
		Severity:    sev,
		Summary:     summary,
		Detail:      detail,
		Timestamp:   time.Now(),
		Fingerprint: event.Fingerprint(c.service, event.SourceProbe, "probe-state"),
	}
	out <- ev
}

func (c *Collector) checkOnce(ctx context.Context) (bool, string) {
	u, err := url.Parse(c.target)
	if err != nil {
		return false, fmt.Sprintf("bad target url: %v", err)
	}
	switch u.Scheme {
	case "http", "https":
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.target, nil)
		resp, err := c.http.Do(req)
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			return true, fmt.Sprintf("status=%d", resp.StatusCode)
		}
		return false, fmt.Sprintf("status=%d", resp.StatusCode)
	case "tcp":
		d := net.Dialer{Timeout: 5 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", u.Host)
		if err != nil {
			return false, err.Error()
		}
		_ = conn.Close()
		return true, "tcp ok"
	default:
		return false, fmt.Sprintf("unsupported scheme %q (use http(s):// or tcp://host:port)", u.Scheme)
	}
}
