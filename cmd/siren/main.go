// Package main is the siren entrypoint.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ishankkm/siren/internal/config"
)

func main() {
	configPath := flag.String("config", "/etc/siren/siren.yaml", "path to YAML config file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

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

func run(ctx context.Context, _ *config.Config, logger *slog.Logger) error {
	logger.Info("siren starting")
	// TODO: wire up collectors -> normalizer -> dedup -> redact -> notifier
	// TODO: start command handler on Discord gateway
	<-ctx.Done()
	logger.Info("siren shutting down")
	return nil
}
