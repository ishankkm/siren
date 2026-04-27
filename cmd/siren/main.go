// Package main is the siren entrypoint.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ishankkm/siren/internal/collector"
	"github.com/ishankkm/siren/internal/collector/journal"
	"github.com/ishankkm/siren/internal/collector/logc"
	"github.com/ishankkm/siren/internal/collector/probe"
	"github.com/ishankkm/siren/internal/collector/proc"
	"github.com/ishankkm/siren/internal/command"
	"github.com/ishankkm/siren/internal/config"
	"github.com/ishankkm/siren/internal/dedup"
	"github.com/ishankkm/siren/internal/event"
	"github.com/ishankkm/siren/internal/notifier"
	"github.com/ishankkm/siren/internal/redact"
	"github.com/ishankkm/siren/internal/state"
)

// Build info, populated via -ldflags by GoReleaser.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	configPath := flag.String("config", "/etc/siren/siren.yaml", "path to YAML config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("siren %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	logger.Info("siren version", "version", version, "commit", commit, "date", date)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "path", *configPath, "err", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, cfg, logger); err != nil {
		logger.Error("siren exited with error", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	token, err := cfg.Token()
	if err != nil {
		return err
	}

	red, err := redact.New(cfg.Redact)
	if err != nil {
		return fmt.Errorf("redact: %w", err)
	}

	store, err := state.Open(filepath.Join(cfg.StateDir, "siren.state.json"))
	if err != nil {
		return fmt.Errorf("state open: %w", err)
	}

	dd := dedup.New(cfg.Dedup.SuppressionWindow, cfg.Dedup.MaxDMPerMinute)

	startedAt := time.Now()

	// Notifier needs a CommandHandler closure that touches store/dedup; but
	// the notifier itself is what we use to Reply, so we declare it first
	// with a nil handler and wire the handler after construction.
	var nf *notifier.Discord
	handler := func(body string) string {
		c, ok := command.Parse(body)
		if !ok {
			return ""
		}
		return handleCommand(c, cfg, store, dd, nf, startedAt)
	}

	nf, err = notifier.New(notifier.Options{
		Token:      token,
		OperatorID: cfg.Operator.DiscordUserID,
		QueueSize:  cfg.Discord.OutboundQueueSize,
		Redactor:   red,
		Logger:     logger,
		Handler:    handler,
	})
	if err != nil {
		return fmt.Errorf("notifier: %w", err)
	}

	collectors := buildCollectors(cfg, logger)
	logger.Info("siren starting",
		"services", len(cfg.Services),
		"collectors", len(collectors),
		"state", cfg.StateDir,
	)

	events := make(chan event.Event, 256)
	var wg sync.WaitGroup

	// Notifier
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := nf.Run(ctx); err != nil {
			logger.Error("notifier exited", "err", err)
		}
	}()

	// Collectors
	for _, c := range collectors {
		c := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Run(ctx, events); err != nil {
				logger.Error("collector exited", "name", c.Name(), "err", err)
			}
		}()
	}

	// Pipeline: events -> mute filter -> dedup -> notifier
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-events:
				now := time.Now()
				if store.IsMuted(ev.Service, now) {
					continue
				}
				switch dd.Check(ev, now) {
				case dedup.DecisionSend:
					nf.Notify(ev)
				case dedup.DecisionSuppress:
					logger.Debug("suppressed", "fp", ev.Fingerprint, "service", ev.Service)
				case dedup.DecisionRateLimited:
					logger.Warn("rate-limited", "fp", ev.Fingerprint, "service", ev.Service)
				}
			}
		}
	}()

	wg.Wait()
	if err := store.Save(); err != nil {
		logger.Warn("state save on exit failed", "err", err)
	}
	logger.Info("siren stopped")
	return nil
}

func buildCollectors(cfg *config.Config, logger *slog.Logger) []collector.Collector {
	var out []collector.Collector
	for _, s := range cfg.Services {
		for _, glob := range s.LogPaths {
			lc, err := logc.New(s.Name, glob, s.LogMatch.Regex, s.LogLevels, logger)
			if err != nil {
				logger.Error("log collector init failed", "service", s.Name, "err", err)
				continue
			}
			out = append(out, lc)
		}
		if s.Process.SystemdUnit != "" {
			out = append(out, proc.New(s.Name, s.Process.SystemdUnit, logger))
		}
		if s.Health.URL != "" {
			out = append(out, probe.New(s.Name, s.Health.URL, s.Health.Interval, logger))
		}
		if s.Journal.Unit != "" {
			pri, _ := s.Journal.JournalPriority() // validated by config.Validate
			jc, err := journal.New(s.Name, s.Journal.Unit, pri, s.Journal.Regex, logger)
			if err != nil {
				logger.Error("journal collector init failed", "service", s.Name, "err", err)
				continue
			}
			out = append(out, jc)
		}
	}
	return out
}

func handleCommand(c command.Command, cfg *config.Config, store *state.Store, dd *dedup.Deduper, nf *notifier.Discord, startedAt time.Time) string {
	switch c.Kind {
	case command.KindHelp:
		return command.HelpText
	case command.KindStatus:
		var b strings.Builder
		fmt.Fprintf(&b, "siren status\n")
		fmt.Fprintf(&b, "  uptime: %s\n", time.Since(startedAt).Round(time.Second))
		fmt.Fprintf(&b, "  queue depth: %d\n", nf.Depth())
		fmt.Fprintf(&b, "  services (%d):\n", len(cfg.Services))
		for _, s := range cfg.Services {
			fmt.Fprintf(&b, "    - %s\n", s.Name)
		}
		mutes := store.Mutes()
		fmt.Fprintf(&b, "  mutes (%d):\n", len(mutes))
		for svc, until := range mutes {
			fmt.Fprintf(&b, "    - %s until %s\n", svc, until.Format(time.RFC3339))
		}
		return b.String()
	case command.KindAck:
		if len(c.Args) < 1 {
			return "usage: !ack <fingerprint>"
		}
		fp := c.Args[0]
		dd.Forget(fp)
		store.Ack(fp, time.Now().Add(24*time.Hour))
		_ = store.Save()
		return fmt.Sprintf("acked %s", fp)
	case command.KindMute:
		if len(c.Args) < 2 {
			return "usage: !mute <service> <duration>"
		}
		svc := c.Args[0]
		dur, err := time.ParseDuration(c.Args[1])
		if err != nil {
			return fmt.Sprintf("bad duration %q: %v", c.Args[1], err)
		}
		until := time.Now().Add(dur)
		store.Mute(svc, until)
		_ = store.Save()
		return fmt.Sprintf("muted %s until %s", svc, until.Format(time.RFC3339))
	case command.KindUnmute:
		if len(c.Args) < 1 {
			return "usage: !unmute <service>"
		}
		store.Unmute(c.Args[0])
		_ = store.Save()
		return fmt.Sprintf("unmuted %s", c.Args[0])
	case command.KindUnknown:
		return "unknown command. !help for usage."
	default:
		return ""
	}
}
