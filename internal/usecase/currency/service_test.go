package currency

import (
	"context"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/cache"
)

type stubProvider struct {
	fetchLatestFn func(ctx context.Context) (domain.DailyRates, error)
	fetchByDateFn func(ctx context.Context, date time.Time) (domain.DailyRates, error)
}

func (p *stubProvider) FetchLatest(ctx context.Context) (domain.DailyRates, error) {
	return p.fetchLatestFn(ctx)
}

func (p *stubProvider) FetchByDate(ctx context.Context, date time.Time) (domain.DailyRates, error) {
	return p.fetchByDateFn(ctx, date)
}

type stubRepo struct {
	mu    sync.Mutex
	rates map[money.Currency]domain.ExchangeRate
}

func newStubRepo() *stubRepo {
	return &stubRepo{rates: make(map[money.Currency]domain.ExchangeRate)}
}

func (r *stubRepo) SaveBatch(ctx context.Context, daily domain.DailyRates) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for curr, rate := range daily.Rates {
		r.rates[curr] = rate
	}
	return nil
}

func (r *stubRepo) GetRate(ctx context.Context, curr money.Currency, date *time.Time) (domain.ExchangeRate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rate, ok := r.rates[curr]
	if !ok {
		return domain.ExchangeRate{}, ErrRateNotFound
	}
	return rate, nil
}

func TestSyncByDateUpdatesCacheOnlyForNewerOrEqualDate(t *testing.T) {
	const usd = money.Currency("USD")

	nov22 := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	nov23 := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	nov20 := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)

	provider := &stubProvider{
		fetchLatestFn: func(ctx context.Context) (domain.DailyRates, error) {
			return domain.DailyRates{
				Date: nov22,
				Rates: map[money.Currency]domain.ExchangeRate{
					usd: {Currency: usd, RateDate: nov22, RUBPerUnit: big.NewRat(90, 1)},
				},
			}, nil
		},
		fetchByDateFn: func(ctx context.Context, date time.Time) (domain.DailyRates, error) {
			if date.Equal(nov23) {
				return domain.DailyRates{
					Date: nov23,
					Rates: map[money.Currency]domain.ExchangeRate{
						usd: {Currency: usd, RateDate: nov23, RUBPerUnit: big.NewRat(95, 1)},
					},
				}, nil
			}
			return domain.DailyRates{
				Date: nov20,
				Rates: map[money.Currency]domain.ExchangeRate{
					usd: {Currency: usd, RateDate: nov20, RUBPerUnit: big.NewRat(85, 1)},
				},
			}, nil
		},
	}

	repo := newStubRepo()
	rateCache := cache.NewRateCache()
	svc := NewService(provider, repo).WithCache(rateCache)

	ctx := context.Background()
	if err := svc.Refresh(ctx); err != nil {
		t.Fatalf("Refresh failed: %v", err)
	}

	if err := svc.SyncByDate(ctx, nov23); err != nil {
		t.Fatalf("SyncByDate(nov23) failed: %v", err)
	}
	got, err := svc.GetRate(ctx, usd, nil)
	if err != nil || got.RUBPerUnit.Cmp(big.NewRat(95, 1)) != 0 {
		t.Fatalf("expected cached rate 95 for nov23, got %v (err=%v)", got, err)
	}

	if err := svc.SyncByDate(ctx, nov20); err != nil {
		t.Fatalf("SyncByDate(nov20) failed: %v", err)
	}
	gotAfterOld, err := svc.GetRate(ctx, usd, nil)
	if err != nil || gotAfterOld.RUBPerUnit.Cmp(big.NewRat(95, 1)) != 0 {
		t.Fatalf("expected cache to keep 95 after syncing historical date, got %v (err=%v)", gotAfterOld, err)
	}
}

func TestRefresherRecoversFromPanicAndContinues(t *testing.T) {
	const usd = money.Currency("USD")
	var calls atomic.Int32
	date := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)

	provider := &stubProvider{
		fetchLatestFn: func(ctx context.Context) (domain.DailyRates, error) {
			if calls.Add(1) == 1 {
				panic("boom in provider")
			}
			return domain.DailyRates{
				Date: date,
				Rates: map[money.Currency]domain.ExchangeRate{
					usd: {Currency: usd, RateDate: date, RUBPerUnit: big.NewRat(95, 1)},
				},
			}, nil
		},
	}

	repo := newStubRepo()
	rateCache := cache.NewRateCache()
	svc := NewService(provider, repo).WithCache(rateCache)

	refresher := NewRefresher(svc, RefresherConfig{
		RefreshInterval: time.Hour,
		RetryInterval:   15 * time.Millisecond,
		RequestTimeout:  time.Second,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- refresher.Run(ctx)
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if rate, ok := rateCache.Get(usd); ok && rate.RUBPerUnit.Cmp(big.NewRat(95, 1)) == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	rate, ok := rateCache.Get(usd)
	if !ok || rate.RUBPerUnit.Cmp(big.NewRat(95, 1)) != 0 {
		t.Fatalf("expected cache to update after panic recovery, got ok=%v rate=%v", ok, rate)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("expected nil on cancel, got %v", err)
	}
}

func TestSlowRefreshDoesNotRollbackNewerSyncByDateCache(t *testing.T) {
	const usd = money.Currency("USD")
	nov24 := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	nov25 := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)

	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})

	provider := &stubProvider{
		fetchLatestFn: func(ctx context.Context) (domain.DailyRates, error) {
			close(refreshStarted)
			<-releaseRefresh
			return domain.DailyRates{
				Date: nov24,
				Rates: map[money.Currency]domain.ExchangeRate{
					usd: {Currency: usd, RateDate: nov24, RUBPerUnit: big.NewRat(90, 1)},
				},
			}, nil
		},
		fetchByDateFn: func(ctx context.Context, date time.Time) (domain.DailyRates, error) {
			return domain.DailyRates{
				Date: nov25,
				Rates: map[money.Currency]domain.ExchangeRate{
					usd: {Currency: usd, RateDate: nov25, RUBPerUnit: big.NewRat(95, 1)},
				},
			}, nil
		},
	}

	repo := newStubRepo()
	rateCache := cache.NewRateCache()
	svc := NewService(provider, repo).WithCache(rateCache)
	ctx := context.Background()

	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- svc.Refresh(ctx)
	}()

	<-refreshStarted

	if err := svc.SyncByDate(ctx, nov25); err != nil {
		t.Fatalf("SyncByDate(nov25) failed: %v", err)
	}

	close(releaseRefresh)
	if err := <-refreshDone; err != nil {
		t.Fatalf("slow Refresh failed: %v", err)
	}

	got, err := svc.GetRate(ctx, usd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.RateDate.Equal(nov25) || got.RUBPerUnit.Cmp(big.NewRat(95, 1)) != 0 {
		t.Fatalf("cache rolled back to stale date: got %v on %v, want 95 on %v", got.RUBPerUnit, got.RateDate, nov25)
	}
}

func TestConcurrentRefreshesCompletingOutOfOrderKeepNewestRate(t *testing.T) {
	const usd = money.Currency("USD")
	nov24 := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	nov25 := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)

	var calls atomic.Int32
	slowStarted := make(chan struct{})
	releaseSlow := make(chan struct{})

	provider := &stubProvider{
		fetchLatestFn: func(ctx context.Context) (domain.DailyRates, error) {
			n := calls.Add(1)
			if n == 1 {
				close(slowStarted)
				<-releaseSlow
				return domain.DailyRates{
					Date: nov24,
					Rates: map[money.Currency]domain.ExchangeRate{
						usd: {Currency: usd, RateDate: nov24, RUBPerUnit: big.NewRat(90, 1)},
					},
				}, nil
			}
			return domain.DailyRates{
				Date: nov25,
				Rates: map[money.Currency]domain.ExchangeRate{
					usd: {Currency: usd, RateDate: nov25, RUBPerUnit: big.NewRat(96, 1)},
				},
			}, nil
		},
	}

	repo := newStubRepo()
	rateCache := cache.NewRateCache()
	svc := NewService(provider, repo).WithCache(rateCache)
	ctx := context.Background()

	slowDone := make(chan error, 1)
	go func() {
		slowDone <- svc.Refresh(ctx)
	}()

	<-slowStarted

	if err := svc.Refresh(ctx); err != nil {
		t.Fatalf("fast newer Refresh failed: %v", err)
	}

	close(releaseSlow)
	if err := <-slowDone; err != nil {
		t.Fatalf("slow older Refresh failed: %v", err)
	}

	got, err := svc.GetRate(ctx, usd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.RateDate.Equal(nov25) || got.RUBPerUnit.Cmp(big.NewRat(96, 1)) != 0 {
		t.Fatalf("cache rolled back after out-of-order Refresh: got %v on %v, want 96 on %v", got.RUBPerUnit, got.RateDate, nov25)
	}
}
