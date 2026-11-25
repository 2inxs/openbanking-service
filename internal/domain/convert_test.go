package domain_test

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
)

func mustAmount(t *testing.T, v string) money.Amount {
	t.Helper()
	a, err := money.Parse(v)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestConvertViaRUBNoIntermediateRounding(t *testing.T) {
	d1 := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)

	// 1 USD = 91.3333 RUB, 1 EUR = 98.7777 RUB
	// 100.00 USD -> EUR без промежуточного округления:
	// 100 * 91.3333 / 98.7777 = 9133.33 / 98.7777 = 92.4634811298... -> 92.4635 (при 4 знаках) и 92.4600 (при 2 знаках)
	usdRate := domain.ExchangeRate{Currency: "USD", RateDate: d1, RUBPerUnit: big.NewRat(913333, 10000)}
	eurRate := domain.ExchangeRate{Currency: "EUR", RateDate: d2, RUBPerUnit: big.NewRat(987777, 10000)}
	src := money.Money{Amount: mustAmount(t, "100.00"), Currency: "USD"}

	res4, err := domain.ConvertViaRUB(src, usdRate, eurRate, 4)
	if err != nil {
		t.Fatalf("ConvertViaRUB(4) failed: %v", err)
	}
	if res4.Target.Currency != "EUR" || res4.Target.Amount.String() != "92.4635" {
		t.Fatalf("unexpected 4-place conversion: %+v", res4.Target)
	}
	if !res4.RateDate.Equal(d1) {
		t.Fatalf("expected conservative earlier RateDate %v, got %v", d1, res4.RateDate)
	}

	res2, err := domain.ConvertViaRUB(src, usdRate, eurRate, 2)
	if err != nil {
		t.Fatalf("ConvertViaRUB(2) failed: %v", err)
	}
	if res2.Target.Amount.String() != "92.4600" {
		t.Fatalf("unexpected 2-place conversion: %s", res2.Target.Amount.String())
	}
}

func TestConvertViaRUBValidationErrors(t *testing.T) {
	d := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	usdRate := domain.ExchangeRate{Currency: "USD", RateDate: d, RUBPerUnit: big.NewRat(90, 1)}
	eurRate := domain.ExchangeRate{Currency: "EUR", RateDate: d, RUBPerUnit: big.NewRat(100, 1)}
	rubSrc := money.Money{Amount: mustAmount(t, "500"), Currency: money.RUB}

	if _, err := domain.ConvertViaRUB(rubSrc, usdRate, eurRate, 4); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("expected ErrCurrencyMismatch, got %v", err)
	}
}
