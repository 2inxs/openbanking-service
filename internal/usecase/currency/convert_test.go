package currency_test

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/cache"
	"github.com/2inxs/openbanking-service/internal/usecase/currency"
)

func TestServiceConvertDirectionsAndEdgeCases(t *testing.T) {
	ctx := context.Background()
	d := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)

	repo := newFakeRepo()
	_ = repo.SaveBatch(ctx, domain.DailyRates{
		Date: d,
		Rates: map[money.Currency]domain.ExchangeRate{
			"USD": {Currency: "USD", RateDate: d, RUBPerUnit: big.NewRat(90, 1)},
			"EUR": {Currency: "EUR", RateDate: d, RUBPerUnit: big.NewRat(100, 1)},
			"JPY": {Currency: "JPY", RateDate: d, RUBPerUnit: big.NewRat(6, 10)}, // 0.60 RUB за 1 JPY
		},
	})

	rateCache := cache.NewRateCache()
	svc := currency.NewService(&fakeProvider{}, repo).WithCache(rateCache)
	if _, err := svc.Warmup(ctx); err != nil {
		t.Fatal(err)
	}

	amt100, _ := money.Parse("100.00")
	amtNeg, _ := money.Parse("-250.50")

	// 1. Foreign -> RUB (100 USD -> 9000.0000 RUB)
	res1, err := svc.Convert(ctx, currency.ConvertRequest{
		Amount: money.Money{Amount: amt100, Currency: "USD"},
		To:     money.RUB,
	})
	if err != nil || res1.Target.Amount.String() != "9000.0000" || res1.Target.Currency != money.RUB {
		t.Fatalf("USD -> RUB failed: err=%v res=%+v", err, res1)
	}

	// 2. RUB -> Foreign (9000 RUB -> 100.0000 USD)
	amt9000, _ := money.Parse("9000")
	res2, err := svc.Convert(ctx, currency.ConvertRequest{
		Amount: money.Money{Amount: amt9000, Currency: money.RUB},
		To:     "USD",
	})
	if err != nil || res2.Target.Amount.String() != "100.0000" || res2.Target.Currency != "USD" {
		t.Fatalf("RUB -> USD failed: err=%v res=%+v", err, res2)
	}

	// 3. Cross-currency Foreign -> Foreign via RUB (-250.50 EUR -> USD = -250.50 * 100 / 90 = -278.333333... -> -278.3333)
	res3, err := svc.Convert(ctx, currency.ConvertRequest{
		Amount: money.Money{Amount: amtNeg, Currency: "EUR"},
		To:     "USD",
	})
	if err != nil || res3.Target.Amount.String() != "-278.3333" || res3.Target.Currency != "USD" {
		t.Fatalf("EUR -> USD negative cross-conversion failed: err=%v res=%+v", err, res3)
	}

	// 4. Same currency (JPY -> JPY)
	res4, err := svc.Convert(ctx, currency.ConvertRequest{
		Amount: money.Money{Amount: amt100, Currency: "JPY"},
		To:     "JPY",
	})
	if err != nil || res4.Target.Amount.String() != "100.0000" || res4.EffectiveRate != "1.0000000000" {
		t.Fatalf("JPY -> JPY failed: err=%v res=%+v", err, res4)
	}

	// 5. Missing rate returns ErrRateNotFound
	if _, err := svc.Convert(ctx, currency.ConvertRequest{
		Amount: money.Money{Amount: amt100, Currency: "GBP"},
		To:     money.RUB,
	}); !errors.Is(err, currency.ErrRateNotFound) {
		t.Fatalf("expected ErrRateNotFound for missing GBP rate, got %v", err)
	}

	// 6. Invalid currency returns ErrInvalidCurrency
	if _, err := svc.Convert(ctx, currency.ConvertRequest{
		Amount: money.Money{Amount: amt100, Currency: "bad"},
		To:     money.RUB,
	}); !errors.Is(err, currency.ErrInvalidCurrency) {
		t.Fatalf("expected ErrInvalidCurrency, got %v", err)
	}

	// 7. Pointer to zero time.Time{} returns domain.ErrInvalidModel
	zeroTime := time.Time{}
	if _, err := svc.Convert(ctx, currency.ConvertRequest{
		Amount: money.Money{Amount: amt100, Currency: "USD"},
		To:     money.RUB,
		Date:   &zeroTime,
	}); !errors.Is(err, domain.ErrInvalidModel) {
		t.Fatalf("expected ErrInvalidModel for zero pointer date, got %v", err)
	}

	// 8. Tiny non-zero amount rounded to zero sets ZeroAfterRounding = true
	tinyRUB, _ := money.Parse("0.0100")
	places2 := 2
	resTiny, err := svc.Convert(ctx, currency.ConvertRequest{
		Amount: money.Money{Amount: tinyRUB, Currency: money.RUB},
		To:     "USD",
		Places: &places2,
	})
	if err != nil || resTiny.Target.Amount.String() != "0.0000" || !resTiny.ZeroAfterRounding {
		t.Fatalf("expected ZeroAfterRounding=true for 0.01 RUB -> USD (2 places), got err=%v res=%+v", err, resTiny)
	}

	// 9. Future date is rejected with domain.ErrInvalidModel
	futureDate := time.Now().UTC().AddDate(1, 0, 0)
	if _, err := svc.Convert(ctx, currency.ConvertRequest{
		Amount: money.Money{Amount: amt100, Currency: "USD"},
		To:     money.RUB,
		Date:   &futureDate,
	}); !errors.Is(err, domain.ErrInvalidModel) {
		t.Fatalf("expected ErrInvalidModel for future date, got %v", err)
	}

	// 10. Excessive big.Int magnitude is rejected to prevent CPU/Memory DoS
	hugeAmt, _ := money.Parse("1000000000000000000000.0000") // 10^21 > 10^20 limit
	if _, err := svc.Convert(ctx, currency.ConvertRequest{
		Amount: money.Money{Amount: hugeAmt, Currency: "USD"},
		To:     money.RUB,
	}); !errors.Is(err, money.ErrInvalidAmount) {
		t.Fatalf("expected ErrInvalidAmount for huge amount DoS guard, got %v", err)
	}
}

func TestServiceConvertSymmetryAcrossDifferentLatestRateDates(t *testing.T) {
	ctx := context.Background()
	nov20 := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	nov24 := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)

	// 20 ноября: USD = 90 RUB, EUR = 100 RUB.
	// 24 ноября: обновлён только EUR до 200 RUB, USD остался от 20 ноября (90 RUB).
	repo := newFakeRepo()
	_ = repo.SaveBatch(ctx, domain.DailyRates{
		Date: nov20,
		Rates: map[money.Currency]domain.ExchangeRate{
			"USD": {Currency: "USD", RateDate: nov20, RUBPerUnit: big.NewRat(90, 1)},
			"EUR": {Currency: "EUR", RateDate: nov20, RUBPerUnit: big.NewRat(100, 1)},
		},
	})
	_ = repo.SaveBatch(ctx, domain.DailyRates{
		Date: nov24,
		Rates: map[money.Currency]domain.ExchangeRate{
			"EUR": {Currency: "EUR", RateDate: nov24, RUBPerUnit: big.NewRat(200, 1)},
		},
	})

	svc := currency.NewService(&fakeProvider{}, repo)

	oneUSD, _ := money.Parse("1.0000")
	// Прямая конвертация без даты: 1 USD -> EUR (90 / 200 = 0.4500 EUR)
	fwd, err := svc.Convert(ctx, currency.ConvertRequest{
		Amount: money.Money{Amount: oneUSD, Currency: "USD"},
		To:     "EUR",
	})
	if err != nil {
		t.Fatalf("USD -> EUR failed: %v", err)
	}
	if fwd.Target.Amount.String() != "0.4500" {
		t.Fatalf("expected 1 USD -> 0.4500 EUR, got %s", fwd.Target.Amount.String())
	}

	// Обратная конвертация без даты: 0.4500 EUR -> USD (0.45 * 200 / 90 = 1.0000 USD)
	back, err := svc.Convert(ctx, currency.ConvertRequest{
		Amount: fwd.Target,
		To:     "USD",
	})
	if err != nil {
		t.Fatalf("EUR -> USD failed: %v", err)
	}
	if back.Target.Amount.String() != "1.0000" {
		t.Fatalf("expected round-trip 0.4500 EUR -> 1.0000 USD, got %s", back.Target.Amount.String())
	}
	if !fwd.RateDate.Equal(back.RateDate) || !fwd.RateDate.Equal(nov20) {
		t.Fatalf("expected symmetric pair RateDate=%v, got fwd=%v back=%v", nov20, fwd.RateDate, back.RateDate)
	}
}
