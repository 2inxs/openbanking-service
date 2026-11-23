package app

import (
	"context"
	"log/slog"

	"github.com/2inxs/openbanking-service/internal/config"
	"github.com/2inxs/openbanking-service/internal/infrastructure/postgres"
)

// Run assembles dependencies. HTTP starts only after PostgreSQL is reachable.
func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if ctx.Err() != nil {
		return nil
	}
	pool, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()
	return New(cfg, logger).Run(ctx)
}
