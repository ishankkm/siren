// Package journal implements a systemd-journald collector.
//
// It subscribes to entries for a single systemd unit by spawning
//
//	journalctl -u <unit> -f -o json --since now -n 0 --no-pager
//
// and parsing one JSON object per line. Filtering is performed against the
// journald PRIORITY field (max-priority threshold) and an optional regex on
// the MESSAGE field. The subprocess is restarted with a small backoff if it
// exits while the context is still alive; in-flight lines during a restart
// are dropped (acceptable for v1 per design).
package journal

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"time"

	"github.com/ishankkm/siren/internal/event"
)

// Collector watches one systemd unit's journal entries.
type Collector struct {
	service     string
	unit        string
	maxPriority int            // keep entries with PRIORITY <= maxPriority
	regex       *regexp.Regexp // optional MESSAGE matcher
	logger      *slog.Logger

	// commandFn is overridable in tests; defaults to journalctlCmd.
	commandFn func(ctx context.Context, unit string) *exec.Cmd
}

// New constructs a journal collector. maxPriority is the inclusive upper
// bound on journald PRIORITY (e.g. 3 for "err and worse"). If regex is ""
// no message-content filtering is applied (priority alone decides).
func New(service, unit string, maxPriority int, regex string, logger *slog.Logger) (*Collector, error) {
	c := &Collector{
		service:     service,
		unit:        unit,
		maxPriority: maxPriority,
		logger:      logger.With("service", service, "collector", "journal", "unit", unit),
		commandFn:   journalctlCmd,
	}
	if regex != "" {
		re, err := regexp.Compile(regex)
		if err != nil {
			return nil, fmt.Errorf("compile journal.regex: %w", err)
		}
		c.regex = re
	}
	return c, nil
}

// Name implements collector.Collector.
func (c *Collector) Name() string {
	return fmt.Sprintf("journal:%s", c.service)
}

// Run streams journal entries until ctx is cancelled.
func (c *Collector) Run(ctx context.Context, out chan<- event.Event) error {
	if _, err := exec.LookPath("journalctl"); err != nil {
		c.logger.Warn("journalctl not found; journal collector disabled")
		<-ctx.Done()
		return nil
	}

	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		if ctx.Err() != nil {
			return nil
		}
		err := c.runOnce(ctx, out)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			c.logger.Warn("journalctl exited; will restart", "err", err, "backoff", backoff)
		} else {
			c.logger.Warn("journalctl exited cleanly; will restart", "backoff", backoff)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (c *Collector) runOnce(ctx context.Context, out chan<- event.Event) error {
	cmd := c.commandFn(ctx, c.unit)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = nil // discard
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start journalctl: %w", err)
	}
	c.logger.Info("journalctl started", "pid", cmd.Process.Pid)

	scanErr := c.scan(ctx, stdout, out)
	waitErr := cmd.Wait()
	if scanErr != nil {
		return scanErr
	}
	return waitErr
}

func (c *Collector) scan(ctx context.Context, r io.Reader, out chan<- event.Event) error {
	sc := bufio.NewScanner(r)
	// journald lines can be large; allow up to 1 MiB per record.
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		ev, ok := c.classify(line)
		if !ok {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case out <- ev:
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("scan stdout: %w", err)
	}
	return nil
}

// classify parses one journald JSON record and decides whether to emit. It
// is pure (no I/O) to make unit-testing trivial without invoking journalctl.
func (c *Collector) classify(line []byte) (event.Event, bool) {
	var rec map[string]any
	if err := json.Unmarshal(line, &rec); err != nil {
		return event.Event{}, false
	}
	pri := journaldPriority(rec)
	if pri > c.maxPriority {
		return event.Event{}, false
	}
	msg := journaldMessage(rec)
	if msg == "" {
		return event.Event{}, false
	}
	if c.regex != nil && !c.regex.MatchString(msg) {
		return event.Event{}, false
	}
	ts := journaldTimestamp(rec)
	if ts.IsZero() {
		ts = time.Now()
	}
	summary := msg
	if len(summary) > 200 {
		summary = summary[:200]
	}
	norm := event.NormalizeSummary(summary)
	return event.Event{
		Service:     c.service,
		Source:      event.SourceJournal,
		Severity:    severityFromPriority(pri),
		Summary:     fmt.Sprintf("[%s] %s", c.unit, summary),
		Detail:      msg,
		Timestamp:   ts,
		Fingerprint: event.Fingerprint(c.service, event.SourceJournal, norm),
	}, true
}

// severityFromPriority maps a syslog/journald PRIORITY (0..7) to an
// event.Severity. Out-of-range values default to SeverityError (safe for an
// alerting tool).
func severityFromPriority(p int) event.Severity {
	switch {
	case p <= 2:
		return event.SeverityCritical
	case p == 3:
		return event.SeverityError
	case p == 4:
		return event.SeverityWarn
	case p >= 5 && p <= 7:
		return event.SeverityInfo
	default:
		return event.SeverityError
	}
}

// journaldPriority extracts PRIORITY from a journald record. journalctl -o
// json renders all values as strings; missing/invalid PRIORITY defaults to 6
// (info), matching journald's own behaviour for entries without an explicit
// level so we don't accidentally over-alert on un-prioritized lines.
func journaldPriority(rec map[string]any) int {
	v, ok := rec["PRIORITY"]
	if !ok {
		return 6
	}
	s, ok := v.(string)
	if !ok {
		return 6
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 6
	}
	return n
}

// journaldMessage extracts MESSAGE, which may be a string or — for binary
// payloads — an array of byte values. Binary messages are rare for typical
// stdout-captured services; we return empty for them.
func journaldMessage(rec map[string]any) string {
	v, ok := rec["MESSAGE"]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// journaldTimestamp parses __REALTIME_TIMESTAMP (microseconds since the
// Unix epoch, as a decimal string). Returns zero on any parse failure so
// the caller can fall back to time.Now().
func journaldTimestamp(rec map[string]any) time.Time {
	v, ok := rec["__REALTIME_TIMESTAMP"]
	if !ok {
		return time.Time{}
	}
	s, ok := v.(string)
	if !ok {
		return time.Time{}
	}
	usec, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMicro(usec)
}

func journalctlCmd(ctx context.Context, unit string) *exec.Cmd {
	return exec.CommandContext(ctx,
		"journalctl",
		"-u", unit,
		"-f",
		"-o", "json",
		"--since", "now",
		"-n", "0",
		"--no-pager",
	)
}
