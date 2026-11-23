package currency_test

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/cache"
	"github.com/2inxs/openbanking-service/internal/usecase/currency"
)

type fakeProvider struct {
	mu      sync.Mutex
	rates   domain.DailyRates
	err     error
	blockCh chan struct{}
	started chan struct{}
}

func (p *fakeProvider) FetchLatest(ctx context.Context) (domain.DailyRates, error) {
	p.mu.Lock()
	block := p.blockCh
	started := p.started
	p.mu.Unlock()

	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return domain.DailyRates{}, ctx.Err()
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rates, p.err
}

func (p *fakeProvider) FetchByDate(ctx context.Context, date time.Time) (domain.DailyRates, error) {
	return p.FetchLatest(ctx)
}

type fakeRepo struct {
	mu      sync.Mutex
	stored  map[money.Currency]domain.ExchangeRate
	saveErr error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{stored: make(map[money.Currency]domain.ExchangeRate)}
}

func (r *fakeRepo) SaveBatch(ctx context.Context, rates domain.DailyRates) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saveErr != nil {
		return r.saveErr
	}
	for k, v := range rates.Rates {
		r.stored[k] = v
	}
	return nil
}

func (r *fakeRepo) GetRate(ctx context.Context, curr money.Currency, date *time.Time) (domain.ExchangeRate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rate, ok := r.stored[curr]
	if !ok {
		return domain.ExchangeRate{}, currency.ErrRateNotFound
	}
	return rate, nil
}

func sampleDaily(date time.Time, usd string) domain.DailyRates {
	rat, _ := new(big.Rat).SetString(usd)
	return domain.DailyRates{
		Date: date,
		Rates: map[money.Currency]domain.ExchangeRate{
			"USD": {Currency: "USD", RateDate: date, RUBPerUnit: rat},
		},
	}
}

func TestRefreshDoesNotUpdateCacheWhenDBSaveFails(t *testing.T) {
	d1 := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)

	repo := newFakeRepo()
	provider := &fakeProvider{rates: sampleDaily(d1, "90.0000")}
	rateCache := cache.NewRateCache()

	svc := currency.NewService(provider, repo).WithCache(rateCache)
	if err := svc.Refresh(context.Background()); err != nil {
		t.Fatalf("initial refresh failed: %v", err)
	}

	provider.rates = sampleDaily(d2, "95.0000")
	repo.saveErr = errors.New("db connection lost")

	if err := svc.Refresh(context.Background()); err == nil {
		t.Fatal("expected error when DB save fails")
	}

	got, err := svc.GetRate(context.Background(), "USD", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.RUBPerUnitString() != "90.0000000000" || !got.RateDate.Equal(d1) {
		t.Fatalf("cache was polluted on DB failure: got %s on %v", got.RUBPerUnitString(), got.RateDate)
	}
}

func TestRefresherKeepsOldRatesOnFailureAndRetries(t *testing.T) {
	d1 := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)

	repo := newFakeRepo()
	_ = repo.SaveBatch(context.Background(), sampleDaily(d1, "88.5000"))

	provider := &fakeProvider{err: errors.New("cbr timeout")}
	rateCache := cache.NewRateCache()
	svc := currency.NewService(provider, repo).WithCache(rateCache)

	trigger := make(chan struct{}, 1)
	refresher := currency.NewRefresher(svc, currency.RefresherConfig{
		RefreshInterval: time.Hour,
		RetryInterval:   time.Hour,
		RequestTimeout:  time.Second,
		TriggerCh:       trigger,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- refresher.Run(ctx) }()

	time.Sleep(30 * time.Millisecond)
	got, err := svc.GetRate(context.Background(), "USD", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.RUBPerUnitString() != "88.5000000000" || !got.RateDate.Equal(d1) {
		t.Fatalf("expected old cached rate 88.5 on %v, got %s on %v", d1, got.RUBPerUnitString(), got.RateDate)
	}

	provider.mu.Lock()
	provider.err = nil
	provider.rates = sampleDaily(d2, "91.2500")
	provider.mu.Unlock()

	trigger <- struct{}{}
	time.Sleep(30 * time.Millisecond)

	got, err = svc.GetRate(context.Background(), "USD", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.RUBPerUnitString() != "91.2500000000" || !got.RateDate.Equal(d2) {
		t.Fatalf("expected refreshed rate 91.25 on %v, got %s on %v", d2, got.RUBPerUnitString(), got.RateDate)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("refresher shutdown returned error: %v", err)
	}
}

func TestRefresherShutdownCancelsInflightRequestAndRaceSafe(t *testing.T) {
	repo := newFakeRepo()
	started := make(chan struct{}, 1)
	provider := &fakeProvider{
		blockCh: make(chan struct{}),
		started: started,
	}
	rateCache := cache.NewRateCache()
	rateCache.Replace(sampleDaily(time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC), "90.0000"), time.Now())

	svc := currency.NewService(provider, repo).WithCache(rateCache)
	refresher := currency.NewRefresher(svc, currency.RefresherConfig{
		RefreshInterval: time.Hour,
		RetryInterval:   time.Hour,
		RequestTimeout:  5 * time.Second,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- refresher.Run(ctx) }()

	<-started

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				r, err := svc.GetRate(context.Background(), "USD", nil)
				if err == nil && r.RUBPerUnit != nil {
					r.RUBPerUnit.SetInt64(999999)
				}
			}
		}()
	}
	wg.Wait()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil on context cancel, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("refresher did not stop promptly on context cancel")
	}
}
