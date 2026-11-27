package currency

import (
	"context"
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/cache"
)

type slowWarmupRepo struct {
	*stubRepo
	blocked atomic.Bool
	started chan struct{}
	release chan struct{}
}

func (r *slowWarmupRepo) GetRate(ctx context.Context, curr money.Currency, date *time.Time) (domain.ExchangeRate, error) {
	rate, err := r.stubRepo.GetRate(ctx, curr, date)
	if curr == "USD" && r.blocked.CompareAndSwap(false, true) {
		close(r.started)
		select {
		case <-r.release:
		case <-ctx.Done():
			return domain.ExchangeRate{}, ctx.Err()
		}
	}
	return rate, err
}

type followerThenLeader struct {
	calls atomic.Int32
	repo  *stubRepo
}

func (c *followerThenLeader) Claim(context.Context, string, time.Duration) (RefreshLease, bool, error) {
	return RefreshLease{}, c.calls.Add(1) > 1, nil
}
func (c *followerThenLeader) Complete(ctx context.Context, _ RefreshLease, daily domain.DailyRates, _ time.Duration) error {
	return c.repo.SaveBatch(ctx, daily)
}
func (*followerThenLeader) Fail(context.Context, RefreshLease, time.Duration) error { return nil }

func TestSlowFollowerDoesNotBlockLeaderOrOverwriteSameDateCorrection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	day := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	batch := func(value int64) domain.DailyRates {
		return domain.DailyRates{Date: day, Rates: map[money.Currency]domain.ExchangeRate{
			"USD": {Currency: "USD", RateDate: day, RUBPerUnit: big.NewRat(value, 1)},
		}}
	}
	repo := &slowWarmupRepo{stubRepo: newStubRepo(), started: make(chan struct{}), release: make(chan struct{})}
	_ = repo.SaveBatch(ctx, batch(90))
	provider := &stubProvider{fetchLatestFn: func(context.Context) (domain.DailyRates, error) { return batch(95), nil }}
	svc := NewService(provider, repo).WithCache(cache.NewRateCache()).WithRefreshCoordinator(
		&followerThenLeader{repo: repo.stubRepo}, time.Hour, time.Minute, time.Minute,
	)
	follower := make(chan error, 1)
	go func() { follower <- svc.Refresh(ctx) }()
	select {
	case <-repo.started:
	case <-ctx.Done():
		t.Fatal("follower did not start reading")
	}
	leader := make(chan error, 1)
	go func() { leader <- svc.Refresh(ctx) }()
	select {
	case err := <-leader:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("slow follower blocked leader refresh")
	}
	// Allow the older read to finish only after the corrected value is published.
	close(repo.release)
	if err := <-follower; err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetRate(ctx, "USD", nil)
	if err != nil || got.RUBPerUnit.Cmp(big.NewRat(95, 1)) != 0 {
		t.Fatalf("same-date correction overwritten by old read: %+v %v", got, err)
	}
}

func TestWarmupDoesNotReplaceNewerCacheWithOlderDate(t *testing.T) {
	ctx := context.Background()
	oldDay := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	newDay := oldDay.AddDate(0, 0, 1)
	repo := newStubRepo()
	_ = repo.SaveBatch(ctx, domain.DailyRates{Date: oldDay, Rates: map[money.Currency]domain.ExchangeRate{
		"USD": {Currency: "USD", RateDate: oldDay, RUBPerUnit: big.NewRat(90, 1)},
	}})
	c := cache.NewRateCache()
	c.Replace(domain.DailyRates{Date: newDay, Rates: map[money.Currency]domain.ExchangeRate{
		"USD": {Currency: "USD", RateDate: newDay, RUBPerUnit: big.NewRat(95, 1)},
	}}, newDay)
	svc := NewService(nil, repo).WithCache(c)
	if _, err := svc.Warmup(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetRate(ctx, "USD", nil)
	if err != nil || !got.RateDate.Equal(newDay) {
		t.Fatalf("cache date regressed: %+v %v", got, err)
	}
}
