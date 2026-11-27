package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/2inxs/openbanking-service/internal/config"
	"github.com/2inxs/openbanking-service/internal/infrastructure/cache"
	"github.com/2inxs/openbanking-service/internal/infrastructure/cbr"
	"github.com/2inxs/openbanking-service/internal/infrastructure/postgres"
	"github.com/2inxs/openbanking-service/internal/usecase/currency"
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

	rateCache := cache.NewRateCache()
	cbrClient := cbr.NewClient()
	rateRepo := postgres.NewRateRepository(pool)

	currencySvc := currency.NewService(cbrClient, rateRepo).WithCache(rateCache)
	currencySvc.WithRefreshCoordinator(postgres.NewRateRefreshCoordinator(pool), cfg.Rates.RefreshInterval, cfg.Rates.RetryInterval, cfg.Rates.RequestTimeout+cfg.Database.StatementTimeout+5*time.Second)
	refresher := currency.NewRefresher(currencySvc, currency.RefresherConfig{
		RefreshInterval: cfg.Rates.RefreshInterval,
		RetryInterval:   cfg.Rates.RetryInterval,
		RequestTimeout:  cfg.Rates.RequestTimeout,
	}, logger)

	application := New(cfg, logger)
	application.AddWorker(refresher.Run)
	return application.Run(ctx)
}
