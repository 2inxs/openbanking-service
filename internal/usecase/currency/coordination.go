package currency

import (
	"context"
	"errors"
	"fmt"
	"github.com/2inxs/openbanking-service/internal/domain"
	"time"
)

var ErrRefreshLeaseLost = errors.New("currency: refresh ownership expired")

type RefreshLease struct {
	Key, Owner string
	Version    int64
}
type RefreshCoordinator interface {
	Claim(context.Context, string, time.Duration) (RefreshLease, bool, error)
	Complete(context.Context, RefreshLease, domain.DailyRates, time.Duration) error
	Fail(context.Context, RefreshLease, time.Duration) error
}

// WithRefreshCoordinator enables shared scheduling for latest and historical refreshes.
func (s *Service) WithRefreshCoordinator(c RefreshCoordinator, interval, retry, lease time.Duration) *Service {
	s.coordinator = c
	s.refreshInterval = interval
	s.retryInterval = retry
	s.leaseDuration = lease
	return s
}

func (s *Service) coordinatedRefresh(ctx context.Context, key string, fetch func(context.Context) (domain.DailyRates, error)) error {
	if s.refreshInterval <= 0 || s.retryInterval <= 0 || s.leaseDuration <= 0 {
		return domain.ErrInvalidModel
	}
	claim, acquired, err := s.coordinator.Claim(ctx, key, s.leaseDuration)
	if err != nil {
		return fmt.Errorf("currency: claim refresh: %w", err)
	}
	if !acquired {
		// Followers read shared storage; contention is not a provider failure.
		s.syncMu.Lock()
		defer s.syncMu.Unlock()
		_, err := s.Warmup(ctx)
		return err
	}
	complete := false
	defer func() {
		if !complete {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.coordinator.Fail(cleanup, claim, s.retryInterval)
		}
	}()
	daily, err := fetch(ctx)
	if err != nil {
		return fmt.Errorf("currency: fetch coordinated rates: %w", err)
	}
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	filtered, err := s.filterSupported(ctx, daily)
	if err != nil {
		return err
	}
	hasRate := false
	for code := range filtered.Rates {
		if code != "RUB" {
			hasRate = true
		}
	}
	if !hasRate {
		return fmt.Errorf("%w: empty supported rate batch", domain.ErrInvalidModel)
	}
	if err := s.coordinator.Complete(ctx, claim, filtered, s.refreshInterval); err != nil {
		return err
	}
	complete = true
	// Reload rounded persisted values; never publish an uncommitted provider result.
	_, err = s.Warmup(ctx)
	return err
}
