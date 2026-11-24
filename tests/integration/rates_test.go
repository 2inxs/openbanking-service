//go:build integration

package integration_test

import (
"context"
"errors"
"math/big"
"testing"
"time"

"github.com/2inxs/openbanking-service/internal/domain"
"github.com/2inxs/openbanking-service/internal/domain/money"
"github.com/2inxs/openbanking-service/internal/infrastructure/cache"
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

func TestRateRepositorySaveBatchGetRateAndWarmupPersistence(t *testing.T) {
ctx := context.Background()
tx := repositoryTx(t)
repo := postgres.NewRateRepository(tx)

day1 := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
day2 := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)

usdDay1 := domain.ExchangeRate{
Currency:   money.Currency("USD"),
RateDate:   day1,
RUBPerUnit: big.NewRat(914567, 10000),
}
eurDay1 := domain.ExchangeRate{
Currency:   money.Currency("EUR"),
RateDate:   day1,
RUBPerUnit: big.NewRat(981234, 10000),
}

if err := repo.SaveBatch(ctx, domain.DailyRates{
Date:  day1,
Rates: map[money.Currency]domain.ExchangeRate{"USD": usdDay1, "EUR": eurDay1},
}); err != nil {
t.Fatalf("SaveBatch day1: %v", err)
}

// Сохраняем курс USD на более позднюю дату (23 ноября) и проверяем upsert.
usdDay2 := domain.ExchangeRate{
Currency:   money.Currency("USD"),
RateDate:   day2,
RUBPerUnit: big.NewRat(9250000000, 100000000),
}
if err := repo.SaveBatch(ctx, domain.DailyRates{
Date:  day2,
Rates: map[money.Currency]domain.ExchangeRate{"USD": usdDay2},
}); err != nil {
t.Fatalf("SaveBatch day2: %v", err)
}

// 1. Запрос без даты возвращает самый свежий курс (за 23 ноября).
latestUSD, err := repo.GetRate(ctx, money.Currency("USD"), nil)
if err != nil {
t.Fatalf("GetRate latest USD: %v", err)
}
if !latestUSD.RateDate.Equal(day2) || latestUSD.RUBPerUnitString() != "92.5000000000" {
t.Fatalf("unexpected latest USD: %+v (%s)", latestUSD, latestUSD.RUBPerUnitString())
}

// 2. Запрос на выходной день (22 ноября) возвращает последний доступный курс <= даты (за 20 ноября).
weekend := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
weekendUSD, err := repo.GetRate(ctx, money.Currency("USD"), &weekend)
if err != nil {
t.Fatalf("GetRate weekend USD: %v", err)
}
if !weekendUSD.RateDate.Equal(day1) || weekendUSD.RUBPerUnitString() != "91.4567000000" {
t.Fatalf("unexpected weekend USD: %+v (%s)", weekendUSD, weekendUSD.RUBPerUnitString())
}

// 3. Запрос до самой ранней даты возвращает ErrRateNotFound.
beforeAll := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
if _, err := repo.GetRate(ctx, money.Currency("USD"), &beforeAll); !errors.Is(err, currency.ErrRateNotFound) {
t.Fatalf("expected ErrRateNotFound before all dates, got %v", err)
}

// 4. Имитируем перезапуск сервиса: новый RateCache прогревается из PostgreSQL через Warmup.
newCache := cache.NewRateCache()
restartedSvc := currency.NewService(stubRateProvider{}, repo).WithCache(newCache)
if _, err := restartedSvc.Warmup(ctx); err != nil {
t.Fatalf("Warmup from postgres: %v", err)
}
warmedUSD, ok := newCache.Get(money.Currency("USD"))
if !ok || warmedUSD.RUBPerUnitString() != "92.5000000000" {
t.Fatalf("Warmup did not restore USD rate from PostgreSQL: ok=%v rate=%+v", ok, warmedUSD)
}
}