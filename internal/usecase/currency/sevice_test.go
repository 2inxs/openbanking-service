package currency

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/cache"
)

func TestSyncByDateUpdatesCacheOnlyForNewerOrEqualDate(t *testing.T) {
	nov22 := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	nov23 := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	nov20 := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)

	provider := &stubProvider{
		fetchLatestFn: func(ctx context.Context) (domain.DailyRates, error) {
			return domain.DailyRates{
				Date: nov22,
				Rates: map[money.Currency]domain.ExchangeRate{
					money.USD: {Currency: money.USD, RateDate: nov22, RUBPerUnit: big.NewRat(90, 1)},
				},
			}, nil
		},
		fetchByDateFn: func(ctx context.Context, date time.Time) (domain.DailyRates, error) {
			if date.Equal(nov23) {
				return domain.DailyRates{
					Date: nov23,
					Rates: map[money.Currency]domain.ExchangeRate{
						money.USD: {Currency: money.USD, RateDate: nov23, RUBPerUnit: big.NewRat(95, 1)},
					},
				}, nil
			}
			return domain.DailyRates{
				Date: nov20,
				Rates: map[money.Currency]domain.ExchangeRate{
					money.USD: {Currency: money.USD, RateDate: nov20, RUBPerUnit: big.NewRat(85, 1)},
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

	// 1. Загрузка более свежей даты (23 ноя) должна обновить кэш с 90 до 95.
	if err := svc.SyncByDate(ctx, nov23); err != nil {
		t.Fatalf("SyncByDate(nov23) failed: %v", err)
	}
	got, err := svc.GetRate(ctx, money.USD, nil)
	if err != nil || got.RUBPerUnit.Cmp(big.NewRat(95, 1)) != 0 {
		t.Fatalf("expected cached rate 95 for nov23, got %v (err=%v)", got, err)
	}

	// 2. Загрузка старой истории (20 ноя) НЕ должна откатывать кэш назад на 85.
	if err := svc.SyncByDate(ctx, nov20); err != nil {
		t.Fatalf("SyncByDate(nov20) failed: %v", err)
	}
	gotAfterOld, err := svc.GetRate(ctx, money.USD, nil)
	if err != nil || gotAfterOld.RUBPerUnit.Cmp(big.NewRat(95, 1)) != 0 {
		t.Fatalf("expected cache to keep 95 after syncing historical date, got %v (err=%v)", gotAfterOld, err)
	}
}
