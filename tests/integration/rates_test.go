//go:build integration

package integration_test

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/cache"
	"github.com/2inxs/openbanking-service/internal/infrastructure/cbr"
	"github.com/2inxs/openbanking-service/internal/infrastructure/postgres"
	"github.com/2inxs/openbanking-service/internal/usecase/currency"
)

type stubRateProvider struct {
	daily domain.DailyRates
}

func (s stubRateProvider) FetchLatest(_ context.Context) (domain.DailyRates, error) {
	return s.daily, nil
}

func (s stubRateProvider) FetchByDate(_ context.Context, _ time.Time) (domain.DailyRates, error) {
	return s.daily, nil
}

func cleanupRatesDates(t *testing.T, ctx context.Context, pool interface {
	Exec(context.Context, string, ...any) (interface{ RowsAffected() int64 }, error)
}) {
	t.Helper()
}

func TestRateRepositoryUpsertSameDateAndDateFallback(t *testing.T) {
	ctx := context.Background()
	pool := repositoryPool(t)
	repo := postgres.NewRateRepository(pool)

	day1 := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM exchange_rates WHERE rate_date IN ($1::date, $2::date)`,
			day1.Format("2006-01-02"), day2.Format("2006-01-02"))
	})

	// 1. Первичная запись на 20 ноября.
	if err := repo.SaveBatch(ctx, domain.DailyRates{
		Date: day1,
		Rates: map[money.Currency]domain.ExchangeRate{
			"USD": {Currency: "USD", RateDate: day1, RUBPerUnit: big.NewRat(914567, 10000)},
			"EUR": {Currency: "EUR", RateDate: day1, RUBPerUnit: big.NewRat(981234, 10000)},
		},
	}); err != nil {
		t.Fatalf("SaveBatch day1: %v", err)
	}

	// 2. Повторная запись той же валюты USD на ту же дату (20 ноября) с новым курсом -> проверка ON CONFLICT DO UPDATE без дублей.
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

	// 3. Запрос на выходной день (22 ноября) возвращает обновлённый курс за 20 ноября (<= 22 ноября).
	weekend := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	weekendUSD, err := repo.GetRate(ctx, money.Currency("USD"), &weekend)
	if err != nil {
		t.Fatalf("GetRate weekend USD: %v", err)
	}
	if !weekendUSD.RateDate.Equal(day1) || weekendUSD.RUBPerUnitString() != "91.9999000000" {
		t.Fatalf("unexpected weekend USD after upsert: %+v (%s)", weekendUSD, weekendUSD.RUBPerUnitString())
	}

	// 4. Запрос до самой ранней даты возвращает ErrRateNotFound.
	beforeAll := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	if _, err := repo.GetRate(ctx, money.Currency("USD"), &beforeAll); !errors.Is(err, currency.ErrRateNotFound) {
		t.Fatalf("expected ErrRateNotFound before all dates, got %v", err)
	}
}

func TestRateRepositorySaveBatchAtomicityAndDateMismatch(t *testing.T) {
	ctx := context.Background()
	pool := repositoryPool(t)
	repo := postgres.NewRateRepository(pool)

	day := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	otherDay := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM exchange_rates WHERE rate_date IN ($1::date, $2::date)`,
			day.Format("2006-01-02"), otherDay.Format("2006-01-02"))
	})

	// Сохраняем исходный курс EUR на 24 ноября.
	if err := repo.SaveBatch(ctx, domain.DailyRates{
		Date: day,
		Rates: map[money.Currency]domain.ExchangeRate{
			"EUR": {Currency: "EUR", RateDate: day, RUBPerUnit: big.NewRat(980000, 10000)},
		},
	}); err != nil {
		t.Fatalf("initial SaveBatch: %v", err)
	}

	// 1. Ошибка валидации на второй валюте (USD имеет отрицательный курс): EUR не должен перезаписаться, новых строк быть не должно.
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

	// 2. Несовпадение даты отдельного курса с daily.Date отклоняет весь пакет целиком.
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

	// 3. Ошибка SQL (отменённый контекст или закрытая транзакция) не оставляет частичных изменений.
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

func TestCBRClientRefreshSavesSupportedCurrenciesToInitialSchemaAndWarmsUp(t *testing.T) {
	ctx := context.Background()
	pool := repositoryPool(t)
	repo := postgres.NewRateRepository(pool)

	cbrDate := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM exchange_rates WHERE rate_date = $1::date`, cbrDate.Format("2006-01-02"))
	})

	// Ответ ЦБ содержит как валюты из начального справочника БД (USD, EUR, JPY), так и отсутствующую в БД AUD.
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

	// Штатный Refresh должен успешно сохранить USD, EUR, JPY, отфильтровав AUD без ошибки FK.
	if err := svc.Refresh(ctx); err != nil {
		t.Fatalf("Refresh with CBR response containing AUD failed: %v", err)
	}

	usd, err := repo.GetRate(ctx, money.Currency("USD"), &cbrDate)
	if err != nil || usd.RUBPerUnitString() != "92.3456000000" {
		t.Fatalf("unexpected USD in DB: err=%v rate=%+v", err, usd)
	}
	jpy, err := repo.GetRate(ctx, money.Currency("JPY"), &cbrDate)
	if err != nil || jpy.RUBPerUnitString() != "0.6150000000" {
		t.Fatalf("unexpected JPY in DB: err=%v rate=%+v", err, jpy)
	}

	// Имитируем перезапуск сервиса: новый RateCache прогревается из PostgreSQL через Warmup.
	restartedCache := cache.NewRateCache()
	restartedSvc := currency.NewService(client, repo).WithCache(restartedCache)
	if _, err := restartedSvc.Warmup(ctx); err != nil {
		t.Fatalf("Warmup from postgres failed: %v", err)
	}
	warmedUSD, ok := restartedCache.Get(money.Currency("USD"))
	if !ok || warmedUSD.RUBPerUnitString() != "92.3456000000" {
		t.Fatalf("Warmup did not restore USD rate: ok=%v rate=%+v", ok, warmedUSD)
	}
}
