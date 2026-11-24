//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/cache"
	"github.com/2inxs/openbanking-service/internal/infrastructure/cbr"
	"github.com/2inxs/openbanking-service/internal/infrastructure/postgres"
	"github.com/2inxs/openbanking-service/internal/usecase/currency"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type publicRatesSnapshot struct {
	count int
	sum   string
}

func snapshotPublicExchangeRates(t *testing.T, ctx context.Context, pool *pgxpool.Pool) publicRatesSnapshot {
	t.Helper()
	var snap publicRatesSnapshot
	if err := pool.QueryRow(ctx, `SELECT COUNT(*), COALESCE(SUM(rub_per_unit), 0)::text FROM public.exchange_rates`).Scan(&snap.count, &snap.sum); err != nil {
		t.Fatalf("snapshot public.exchange_rates: %v", err)
	}
	return snap
}

func isolatedRatesPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	adminPool := repositoryPool(t)
	beforeSnap := snapshotPublicExchangeRates(t, ctx, adminPool)

	schema := "test_rates_" + strings.ReplaceAll(newID(t), "-", "")
	setupSQL := fmt.Sprintf(`
CREATE SCHEMA %s;
CREATE TABLE %s.currencies (
code varchar(3) PRIMARY KEY
);
INSERT INTO %s.currencies (code) VALUES ('RUB'), ('USD'), ('EUR'), ('JPY');
CREATE TABLE %s.exchange_rates (
currency_code varchar(3) NOT NULL REFERENCES %s.currencies(code),
rate_date date NOT NULL,
rub_per_unit numeric(24,10) NOT NULL,
fetched_at timestamptz NOT NULL DEFAULT (now()),
CONSTRAINT exchange_rates_positive_finite CHECK (rub_per_unit > 0 AND rub_per_unit NOT IN ('NaN'::numeric, 'Infinity'::numeric, '-Infinity'::numeric)),
PRIMARY KEY (currency_code, rate_date)
);`, schema, schema, schema, schema, schema)

	if _, err := adminPool.Exec(ctx, setupSQL); err != nil {
		t.Fatalf("create isolated schema %s: %v", schema, err)
	}

	cfg := databaseConfig(t)
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	poolCfg.MaxConns = 4
	poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, fmt.Sprintf(`SET search_path TO %s, public`, schema))
		return err
	}

	isolatedPool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatalf("create isolated pool: %v", err)
	}

	t.Cleanup(func() {
		isolatedPool.Close()

		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, err := adminPool.Exec(cleanupCtx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema)); err != nil {
			t.Errorf("drop isolated schema %s: %v", schema, err)
		}

		afterSnap := snapshotPublicExchangeRates(t, cleanupCtx, adminPool)
		if afterSnap != beforeSnap {
			t.Errorf("public.exchange_rates changed during test: before=%+v after=%+v", beforeSnap, afterSnap)
		}
	})

	return isolatedPool
}

func TestRateRepositoryUpsertSameDateAndDateFallback(t *testing.T) {
	ctx := context.Background()
	pool := isolatedRatesPool(t)
	repo := postgres.NewRateRepository(pool)

	day1 := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)

	if err := repo.SaveBatch(ctx, domain.DailyRates{
		Date: day1,
		Rates: map[money.Currency]domain.ExchangeRate{
			"USD": {Currency: "USD", RateDate: day1, RUBPerUnit: big.NewRat(914567, 10000)},
			"EUR": {Currency: "EUR", RateDate: day1, RUBPerUnit: big.NewRat(981234, 10000)},
		},
	}); err != nil {
		t.Fatalf("SaveBatch day1: %v", err)
	}

	if err := repo.SaveBatch(ctx, domain.DailyRates{
		Date: day1,
		Rates: map[money.Currency]domain.ExchangeRate{
			"USD": {Currency: "USD", RateDate: day1, RUBPerUnit: big.NewRat(919999, 10000)},
		},
	}); err != nil {
		t.Fatalf("SaveBatch upsert same date: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM exchange_rates WHERE currency_code = 'USD' AND rate_date = $1::date`,
		day1.Format("2006-01-02")).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row for USD on %s after upsert, got %d", day1.Format("2006-01-02"), count)
	}

	weekend := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	weekendUSD, err := repo.GetRate(ctx, money.Currency("USD"), &weekend)
	if err != nil {
		t.Fatalf("GetRate weekend USD: %v", err)
	}
	if !weekendUSD.RateDate.Equal(day1) || weekendUSD.RUBPerUnitString() != "91.9999000000" {
		t.Fatalf("unexpected weekend USD after upsert: %+v (%s)", weekendUSD, weekendUSD.RUBPerUnitString())
	}

	beforeAll := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	if _, err := repo.GetRate(ctx, money.Currency("USD"), &beforeAll); !errors.Is(err, currency.ErrRateNotFound) {
		t.Fatalf("expected ErrRateNotFound before all dates, got %v", err)
	}
}

func TestRateRepositorySaveBatchAtomicityAndDateMismatch(t *testing.T) {
	ctx := context.Background()
	pool := isolatedRatesPool(t)
	repo := postgres.NewRateRepository(pool)

	day := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	otherDay := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)

	if err := repo.SaveBatch(ctx, domain.DailyRates{
		Date: day,
		Rates: map[money.Currency]domain.ExchangeRate{
			"EUR": {Currency: "EUR", RateDate: day, RUBPerUnit: big.NewRat(980000, 10000)},
		},
	}); err != nil {
		t.Fatalf("initial SaveBatch: %v", err)
	}

	err := repo.SaveBatch(ctx, domain.DailyRates{
		Date: day,
		Rates: map[money.Currency]domain.ExchangeRate{
			"EUR": {Currency: "EUR", RateDate: day, RUBPerUnit: big.NewRat(999900, 10000)},
			"USD": {Currency: "USD", RateDate: day, RUBPerUnit: big.NewRat(-10, 1)},
		},
	})
	if err == nil {
		t.Fatal("expected validation error on invalid second currency")
	}

	eurAfterValidationErr, err := repo.GetRate(ctx, money.Currency("EUR"), &day)
	if err != nil {
		t.Fatal(err)
	}
	if eurAfterValidationErr.RUBPerUnitString() != "98.0000000000" {
		t.Fatalf("EUR was partially updated on validation error: got %s, want 98.0000000000", eurAfterValidationErr.RUBPerUnitString())
	}

	err = repo.SaveBatch(ctx, domain.DailyRates{
		Date: day,
		Rates: map[money.Currency]domain.ExchangeRate{
			"EUR": {Currency: "EUR", RateDate: day, RUBPerUnit: big.NewRat(995000, 10000)},
			"USD": {Currency: "USD", RateDate: otherDay, RUBPerUnit: big.NewRat(920000, 10000)},
		},
	})
	if !errors.Is(err, domain.ErrInvalidModel) {
		t.Fatalf("expected ErrInvalidModel on mismatched rate date, got %v", err)
	}

	eurAfterDateErr, err := repo.GetRate(ctx, money.Currency("EUR"), &day)
	if err != nil {
		t.Fatal(err)
	}
	if eurAfterDateErr.RUBPerUnitString() != "98.0000000000" {
		t.Fatalf("EUR changed after mismatched date rejection: got %s", eurAfterDateErr.RUBPerUnitString())
	}
	if _, err := repo.GetRate(ctx, money.Currency("USD"), &day); !errors.Is(err, currency.ErrRateNotFound) {
		t.Fatalf("USD should not exist after rejected batch, got %v", err)
	}

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := repo.SaveBatch(canceledCtx, domain.DailyRates{
		Date: day,
		Rates: map[money.Currency]domain.ExchangeRate{
			"EUR": {Currency: "EUR", RateDate: day, RUBPerUnit: big.NewRat(970000, 10000)},
		},
	}); err == nil {
		t.Fatal("expected error on canceled context")
	}
}

func TestCBRClientRefreshConsistentBetweenDBAndCacheAcrossRestart(t *testing.T) {
	ctx := context.Background()
	pool := isolatedRatesPool(t)
	repo := postgres.NewRateRepository(pool)

	cbrDate := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)

	xmlPayload := `<?xml version="1.0" encoding="UTF-8"?>
<ValCurs Date="25.11.2026" name="Foreign Currency Market">
<Valute ID="R01010">
<NumCode>036</NumCode>
<CharCode>AUD</CharCode>
<Nominal>1</Nominal>
<Name>Австралийский доллар</Name>
<Value>59,1234</Value>
<VunitRate>59,1234</VunitRate>
</Valute>
<Valute ID="R01235">
<NumCode>840</NumCode>
<CharCode>USD</CharCode>
<Nominal>1</Nominal>
<Name>Доллар США</Name>
<Value>92,3456</Value>
<VunitRate>92,3456</VunitRate>
</Valute>
<Valute ID="R01239">
<NumCode>978</NumCode>
<CharCode>EUR</CharCode>
<Nominal>1</Nominal>
<Name>Евро</Name>
<Value>99,8765</Value>
<VunitRate>99,8765</VunitRate>
</Valute>
<Valute ID="R01820">
<NumCode>392</NumCode>
<CharCode>JPY</CharCode>
<Nominal>100</Nominal>
<Name>Японских иен</Name>
<Value>61,5000</Value>
<VunitRate>0,6150</VunitRate>
</Valute>
</ValCurs>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		_, _ = w.Write([]byte(xmlPayload))
	}))
	defer srv.Close()

	client := cbr.NewClient(cbr.WithEndpoint(srv.URL))
	rateCache := cache.NewRateCache()
	svc := currency.NewService(client, repo).WithCache(rateCache)

	if err := svc.Refresh(ctx); err != nil {
		t.Fatalf("Refresh with CBR response containing AUD failed: %v", err)
	}

	usdLatest, err := svc.GetRate(ctx, money.Currency("USD"), nil)
	if err != nil || usdLatest.RUBPerUnitString() != "92.3456000000" {
		t.Fatalf("unexpected USD latest via service: err=%v rate=%+v", err, usdLatest)
	}
	jpyByDate, err := svc.GetRate(ctx, money.Currency("JPY"), &cbrDate)
	if err != nil || jpyByDate.RUBPerUnitString() != "0.6150000000" {
		t.Fatalf("unexpected JPY by date via service: err=%v rate=%+v", err, jpyByDate)
	}

	if _, err := svc.GetRate(ctx, money.Currency("AUD"), nil); !errors.Is(err, currency.ErrRateNotFound) {
		t.Fatalf("expected ErrRateNotFound for AUD without date (must not be in cache), got %v", err)
	}
	if _, err := svc.GetRate(ctx, money.Currency("AUD"), &cbrDate); !errors.Is(err, currency.ErrRateNotFound) {
		t.Fatalf("expected ErrRateNotFound for AUD by date, got %v", err)
	}

	restartedCache := cache.NewRateCache()
	restartedSvc := currency.NewService(client, repo).WithCache(restartedCache)
	if _, err := restartedSvc.Warmup(ctx); err != nil {
		t.Fatalf("Warmup from postgres failed: %v", err)
	}
	warmedUSD, err := restartedSvc.GetRate(ctx, money.Currency("USD"), nil)
	if err != nil || warmedUSD.RUBPerUnitString() != "92.3456000000" {
		t.Fatalf("unexpected USD after restart: err=%v rate=%+v", err, warmedUSD)
	}
	if _, err := restartedSvc.GetRate(ctx, money.Currency("AUD"), nil); !errors.Is(err, currency.ErrRateNotFound) {
		t.Fatalf("expected ErrRateNotFound for AUD after restart, got %v", err)
	}
}
