package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/2inxs/openbanking-service/internal/app"
	"github.com/2inxs/openbanking-service/internal/config"
	"github.com/2inxs/openbanking-service/pkg/logger"
)

func main() {
	bootstrapLog := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	cfg, err := config.Load()
	if err != nil {
		bootstrapLog.Error("load configuration", "error", err)
		os.Exit(1)
	}

	log, err := logger.New(cfg.LogLevel, os.Stderr)
	if err != nil {
		bootstrapLog.Error("initialize logger", "error", err)
		os.Exit(1)
	}
	slog.SetDefault(log)

	if err := run(cfg, log); err != nil {
		log.Error("API stopped with an error", "error", err)
		os.Exit(1)
	}
}

func run(cfg config.Config, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.New(cfg, log).Run(ctx)
}
