// Package logc implements the log-tail collector.
//
// It watches one or more files (resolved from a single config glob), detects
// per-file whether the format is JSON-lines or plain text, and emits an event
// for every line that crosses the configured severity threshold.
package logc

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nxadm/tail"

	"github.com/ishankkm/siren/internal/event"
)

// Collector tails a set of files for one logical service.
type Collector struct {
	service string
	glob    string
	regex   *regexp.Regexp // plain-text matcher, may be nil
	levels  map[string]struct{}
	logger  *slog.Logger
}

// New constructs a log collector. If regex is "" no plain-text matching is
// performed (only JSON-line sources will produce events). If levels is empty
// it defaults to {"error","fatal","critical","panic"}.
func New(service, glob, regex string, levels []string, logger *slog.Logger) (*Collector, error) {
	c := &Collector{
		service: service,
		glob:    glob,
		levels:  make(map[string]struct{}),
		logger:  logger.With("service", service, "collector", "log"),
	}
	if regex != "" {
		re, err := regexp.Compile(regex)
		if err != nil {
			return nil, fmt.Errorf("compile log_match.regex: %w", err)
		}
		c.regex = re
	}
	if len(levels) == 0 {
		levels = []string{"error", "fatal", "critical", "panic"}
	}
	for _, l := range levels {
		c.levels[strings.ToLower(l)] = struct{}{}
	}
	return c, nil
}

// Name implements collector.Collector.
func (c *Collector) Name() string {
	return fmt.Sprintf("log:%s", c.service)
}

// Run resolves the glob and starts a tailer per matched file. New files
// appearing later are not picked up in v1; rotation of an existing file is
// handled by tail's ReOpen.
func (c *Collector) Run(ctx context.Context, out chan<- event.Event) error {
	matches, err := filepath.Glob(c.glob)
	if err != nil {
		return fmt.Errorf("glob %q: %w", c.glob, err)
	}
	if len(matches) == 0 {
		c.logger.Warn("no files matched glob", "glob", c.glob)
	}

	var wg sync.WaitGroup
	for _, path := range matches {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			c.tailFile(ctx, p, out)
		}(path)
	}
	wg.Wait()
	return nil
}

func (c *Collector) tailFile(ctx context.Context, path string, out chan<- event.Event) {
	t, err := tail.TailFile(path, tail.Config{
		Follow:    true,
		ReOpen:    true,
		MustExist: false,
		Location:  &tail.SeekInfo{Whence: 2}, // EOF on startup
		Logger:    tail.DiscardingLogger,
	})
	if err != nil {
		c.logger.Error("tail open failed", "path", path, "err", err)
		return
	}
	defer func() {
		_ = t.Stop()
		t.Cleanup()
	}()

	// Format auto-detect: locked in by the first non-empty line.
	var detected bool
	var isJSON bool

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-t.Lines:
			if !ok {
				return
			}
			if line.Err != nil {
				c.logger.Warn("tail error", "path", path, "err", line.Err)
				continue
			}
			text := line.Text
			if strings.TrimSpace(text) == "" {
				continue
			}
			if !detected {
				isJSON = looksLikeJSONObject(text)
				detected = true
				c.logger.Info("log format detected", "path", path, "json", isJSON)
			}

			ev, ok := c.classify(path, text, isJSON, line.Time)
			if !ok {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case out <- ev:
			}
		}
	}
}

func (c *Collector) classify(path, text string, isJSON bool, ts time.Time) (event.Event, bool) {
	if ts.IsZero() {
		ts = time.Now()
	}
	if isJSON {
		var fields map[string]any
		if err := json.Unmarshal([]byte(text), &fields); err != nil {
			// Not parseable: skip silently to avoid noisy false positives.
			return event.Event{}, false
		}
		level := firstString(fields, "level", "lvl", "severity")
		if level == "" {
			return event.Event{}, false
		}
		if _, want := c.levels[strings.ToLower(level)]; !want {
			return event.Event{}, false
		}
		msg := firstString(fields, "msg", "message", "error")
		if msg == "" {
			msg = text
		}
		return c.makeEvent(path, msg, text, event.ParseSeverity(level), ts), true
	}

	if c.regex == nil {
		return event.Event{}, false
	}
	if !c.regex.MatchString(text) {
		return event.Event{}, false
	}
	sev := event.SeverityError
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "panic"), strings.Contains(lower, "fatal"), strings.Contains(lower, "critical"):
		sev = event.SeverityCritical
	}
	return c.makeEvent(path, text, text, sev, ts), true
}

func (c *Collector) makeEvent(path, summary, detail string, sev event.Severity, ts time.Time) event.Event {
	if len(summary) > 200 {
		summary = summary[:200]
	}
	norm := event.NormalizeSummary(summary)
	return event.Event{
		Service:     c.service,
		Source:      event.SourceLog,
		Severity:    sev,
		Summary:     fmt.Sprintf("[%s] %s", filepath.Base(path), summary),
		Detail:      detail,
		Timestamp:   ts,
		Fingerprint: event.Fingerprint(c.service, event.SourceLog, norm),
	}
}

func looksLikeJSONObject(s string) bool {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") {
		return false
	}
	var probe map[string]any
	return json.Unmarshal([]byte(s), &probe) == nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}
