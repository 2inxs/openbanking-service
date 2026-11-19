package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/2inxs/openbanking-service/internal/app"
	"github.com/2inxs/openbanking-service/internal/config"
	"github.com/2inxs/openbanking-service/internal/infrastructure/logger"
)

func main() {
	// Инициализируем базовый техлоггер до загрузки конфига (на случай ошибки парсинга env)
	log, err := logger.New(os.Getenv("LOG_LEVEL"), os.Stderr)
	if err != nil {
		// Если в LOG_LEVEL передано невалидное значение, пишем ошибку через дефолтный JSON-логгер
		fallback := slog.New(slog.NewJSONHandler(os.Stderr, nil))
		fallback.Error("failed to initialize logger", "error", err)
		os.Exit(1)
	}
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("API stopped with an error", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.New(cfg, log).Run(ctx)
}
