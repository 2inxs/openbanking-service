package currency

import (
	"context"
	"fmt"
	"log/slog"
	"time"
	_ "time/tzdata"
)

var moscowLoc = mustLoadMoscow()

func mustLoadMoscow() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.FixedZone("MSK", 3*60*60)
	}
	return loc
}

func isStale(rateDate, now time.Time) bool {
	if rateDate.IsZero() {
		return true
	}
	nowMSK := now.In(moscowLoc)
	todayMSK := time.Date(nowMSK.Year(), nowMSK.Month(), nowMSK.Day(), 0, 0, 0, 0, time.UTC)
	rateDay := time.Date(rateDate.Year(), rateDate.Month(), rateDate.Day(), 0, 0, 0, 0, time.UTC)
	return rateDay.Before(todayMSK)
}

type RefresherConfig struct {
	RefreshInterval time.Duration
	RetryInterval   time.Duration
	RequestTimeout  time.Duration
	// TriggerCh используется в тестах для мгновенного запуска тика без ожидания таймера.
	TriggerCh <-chan struct{}
}

type Refresher struct {
	service *Service
	cfg     RefresherConfig
	logger  *slog.Logger
}

func NewRefresher(service *Service, cfg RefresherConfig, logger *slog.Logger) *Refresher {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = time.Hour
	}
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = 5 * time.Minute
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 10 * time.Second
	}
	return &Refresher{
		service: service,
		cfg:     cfg,
		logger:  logger,
	}
}

// Run запускает фоновое обновление курсов до отмены ctx.
func (r *Refresher) Run(ctx context.Context) error {
	needsRefresh, err := r.warmupOnce(ctx)
	if err != nil {
		r.logger.Warn("currency warmup failed, will refresh from provider", "error", err)
		needsRefresh = true
	}

	nextDelay := r.cfg.RefreshInterval
	if needsRefresh {
		if err := r.refreshOnce(ctx); err != nil {
			nextDelay = r.cfg.RetryInterval
		}
	}

	timer := time.NewTimer(nextDelay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.cfg.TriggerCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			if err := r.refreshOnce(ctx); err != nil {
				timer.Reset(r.cfg.RetryInterval)
			} else {
				timer.Reset(r.cfg.RefreshInterval)
			}
		case <-timer.C:
			if err := r.refreshOnce(ctx); err != nil {
				timer.Reset(r.cfg.RetryInterval)
			} else {
				timer.Reset(r.cfg.RefreshInterval)
			}
		}
	}
}

func (r *Refresher) warmupOnce(ctx context.Context) (needsRefresh bool, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			r.logger.Error("currency warmup panic recovered", "panic", fmt.Sprint(rec))
			needsRefresh = true
			err = fmt.Errorf("currency warmup panic: %v", rec)
		}
	}()
	return r.service.Warmup(ctx)
}

func (r *Refresher) refreshOnce(parentCtx context.Context) (err error) {
	if parentCtx.Err() != nil {
		return parentCtx.Err()
	}

	defer func() {
		if rec := recover(); rec != nil {
			r.logger.Error("currency refresher panic recovered", "panic", fmt.Sprint(rec))
			err = fmt.Errorf("currency refresh panic: %v", rec)
		}
	}()

	reqCtx, cancel := context.WithTimeout(parentCtx, r.cfg.RequestTimeout)
	defer cancel()

	if err := r.service.Refresh(reqCtx); err != nil {
		r.logger.Warn("currency refresh failed, keeping cached rates", "error", err)
		return err
	}
	r.logger.Info("currency rates refreshed")
	return nil
}
