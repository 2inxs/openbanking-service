//go:build integration

package integration_test

import (
	"context"
	"errors"
	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/cache"
	"github.com/2inxs/openbanking-service/internal/infrastructure/cbr"
	"github.com/2inxs/openbanking-service/internal/infrastructure/postgres"
	"github.com/2inxs/openbanking-service/internal/usecase/currency"
	"github.com/jackc/pgx/v5/pgxpool"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func refreshPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := isolatedRatesPool(t)
	migration, err := os.ReadFile("../../migrations/000003_rate_refresh_leases.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), string(migration)); err != nil {
		t.Fatal(err)
	}
	return pool
}
func coordinatedDaily(value int64) domain.DailyRates {
	d := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	return domain.DailyRates{Date: d, Rates: map[money.Currency]domain.ExchangeRate{"USD": {Currency: "USD", RateDate: d, RUBPerUnit: big.NewRat(value, 1)}}}
}
func TestDistributedRefreshSixtyServicesOneRequest(t *testing.T) {
	pool := refreshPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`<ValCurs Date="27.11.2026"><Valute><CharCode>USD</CharCode><Nominal>1</Nominal><Value>90,12345678901</Value></Valute></ValCurs>`))
	}))
	defer srv.Close()
	services := make([]*currency.Service, 60)
	for i := range services {
		services[i] = currency.NewService(cbr.NewClient(cbr.WithEndpoint(srv.URL)), postgres.NewRateRepository(pool)).WithCache(cache.NewRateCache()).WithRefreshCoordinator(postgres.NewRateRefreshCoordinator(pool), time.Hour, 5*time.Minute, 15*time.Second)
	}
	run := func() {
		var wg sync.WaitGroup
		errs := make(chan error, 60)
		for _, svc := range services {
			wg.Add(1)
			go func() { defer wg.Done(); errs <- svc.Refresh(ctx) }()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	run()
	run() // follower poll after winner completes must not fetch again.
	if calls.Load() != 1 {
		t.Fatalf("CBR calls=%d, want 1", calls.Load())
	}
	for _, svc := range services {
		r, err := svc.GetRate(ctx, "USD", nil)
		if err != nil || r.RUBPerUnitString() != "90.1234567890" {
			t.Fatalf("follower cache: %+v %v", r, err)
		}
	}
}
func TestDistributedRefreshExpiryFencingAndBackoff(t *testing.T) {
	pool := refreshPool(t)
	ctx := context.Background()
	first := postgres.NewRateRefreshCoordinator(pool)
	second := postgres.NewRateRefreshCoordinator(pool)
	old, ok, err := first.Claim(ctx, "cbr:latest", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	_, ok, err = second.Claim(ctx, "cbr:latest", time.Minute)
	if err != nil || ok {
		t.Fatalf("simultaneous owner: %v %v", ok, err)
	}
	// Simulate a dead process: no release, DB clock moves lease into the past.
	if _, err := pool.Exec(ctx, `UPDATE rate_refresh_leases SET lease_until=clock_timestamp()-interval '1 second' WHERE job_key='cbr:latest'`); err != nil {
		t.Fatal(err)
	}
	current, ok, err := second.Claim(ctx, "cbr:latest", time.Minute)
	if err != nil || !ok || current.Version <= old.Version || current.Owner == old.Owner {
		t.Fatalf("takeover %+v %v %v", current, ok, err)
	}
	if err := first.Complete(ctx, old, coordinatedDaily(50), time.Hour); !errors.Is(err, currency.ErrRefreshLeaseLost) {
		t.Fatalf("expired owner saved: %v", err)
	}
	if err := second.Complete(ctx, current, coordinatedDaily(95), time.Hour); err != nil {
		t.Fatal(err)
	}
	// A delayed cleanup after an ambiguous commit must not shorten a successful schedule.
	if err := second.Fail(ctx, current, time.Microsecond); err != nil {
		t.Fatal(err)
	}
	if err := first.Fail(ctx, old, time.Microsecond); err != nil {
		t.Fatal(err)
	}
	_, ok, err = first.Claim(ctx, "cbr:latest", time.Minute)
	if err != nil || ok {
		t.Fatalf("old owner cleared new schedule: %v %v", ok, err)
	}
	got, err := postgres.NewRateRepository(pool).GetRate(ctx, "USD", nil)
	if err != nil || got.RUBPerUnitString() != "95.0000000000" {
		t.Fatalf("fenced data: %+v %v", got, err)
	}
	failure, ok, err := first.Claim(ctx, "failure", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err := first.Fail(ctx, failure, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	_, ok, err = second.Claim(ctx, "failure", time.Minute)
	if err != nil || ok {
		t.Fatalf("retry backoff bypassed: %v %v", ok, err)
	}
	// A retry after the DB-defined deadline can acquire a new lease.
	if _, err := pool.Exec(ctx, `UPDATE rate_refresh_leases SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE job_key='failure'`); err != nil {
		t.Fatal(err)
	}
	_, ok, err = second.Claim(ctx, "failure", time.Minute)
	if err != nil || !ok {
		t.Fatalf("retry did not recover: %v %v", ok, err)
	}
}

func TestDistributedRefreshProviderFailureHasSharedBackoff(t *testing.T) {
	pool := refreshPool(t)
	ctx := context.Background()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	for i := 0; i < 60; i++ {
		svc := currency.NewService(cbr.NewClient(cbr.WithEndpoint(srv.URL)), postgres.NewRateRepository(pool)).WithCache(cache.NewRateCache()).WithRefreshCoordinator(postgres.NewRateRefreshCoordinator(pool), time.Hour, 5*time.Minute, time.Minute)
		err := svc.Refresh(ctx)
		if i == 0 && err == nil {
			t.Fatal("provider failure was hidden")
		}
		if i > 0 && err != nil {
			t.Fatalf("lease contention reported as provider failure: %v", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("failed provider called %d times, want 1", calls.Load())
	}
}
