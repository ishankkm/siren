// Package proc implements the process / systemd-unit watcher collector.
package proc

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/ishankkm/siren/internal/event"
)

// Collector polls a systemd unit's ActiveState/SubState/Result and emits an
// event when the unit becomes failed or inactive after being active.
type Collector struct {
	service string
	unit    string
	logger  *slog.Logger
	period  time.Duration
}

// New constructs a process collector for a systemd unit.
func New(service, unit string, logger *slog.Logger) *Collector {
	return &Collector{
		service: service,
		unit:    unit,
		logger:  logger.With("service", service, "collector", "process", "unit", unit),
		period:  10 * time.Second,
	}
}

// Name implements collector.Collector.
func (c *Collector) Name() string {
	return fmt.Sprintf("process:%s", c.service)
}

// Run polls until ctx is cancelled.
func (c *Collector) Run(ctx context.Context, out chan<- event.Event) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		c.logger.Warn("systemctl not found; process collector disabled")
		<-ctx.Done()
		return nil
	}

	t := time.NewTicker(c.period)
	defer t.Stop()

	var prevActive, prevResult string
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}

		active, sub, result, err := c.queryUnit(ctx)
		if err != nil {
			c.logger.Warn("systemctl show failed", "err", err)
			continue
		}
		// Emit on transition into a bad state.
		bad := active == "failed" || (active == "inactive" && result != "" && result != "success")
		prevBad := prevActive == "failed" || (prevActive == "inactive" && prevResult != "" && prevResult != "success")
		if bad && !prevBad {
			summary := fmt.Sprintf("unit %s entered %s/%s (result=%s)", c.unit, active, sub, result)
			ev := event.Event{
				Service:     c.service,
				Source:      event.SourceProcess,
				Severity:    event.SeverityCritical,
				Summary:     summary,
				Detail:      fmt.Sprintf("ActiveState=%s SubState=%s Result=%s", active, sub, result),
				Timestamp:   time.Now(),
				Fingerprint: event.Fingerprint(c.service, event.SourceProcess, "unit-failed"),
			}
			select {
			case <-ctx.Done():
				return nil
			case out <- ev:
			}
		}
		prevActive, prevResult = active, result
	}
}

func (c *Collector) queryUnit(ctx context.Context) (active, sub, result string, err error) {
	cmd := exec.CommandContext(ctx, "systemctl", "show", c.unit,
		"--property=ActiveState", "--property=SubState", "--property=Result", "--no-pager")
	out, err := cmd.Output()
	if err != nil {
		return "", "", "", err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "ActiveState":
			active = v
		case "SubState":
			sub = v
		case "Result":
			result = v
		}
	}
	return active, sub, result, nil
}
