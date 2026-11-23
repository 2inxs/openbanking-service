package memory_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/memory"
)

func TestRateRepository_ClonesBigRatOnSaveAndGet(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewRateRepository()
	rateDate := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)

	inputRat := big.NewRat(90, 1)
	err := repo.SaveBatch(ctx, domain.DailyRates{
		Date: rateDate,
		Rates: map[money.Currency]domain.ExchangeRate{
			"USD": {
				Currency:   "USD",
				RateDate:   rateDate,
				RUBPerUnit: inputRat,
			},
		},
	})
	if err != nil {
		t.Fatalf("SaveBatch failed: %v", err)
	}

	// 1. Мутируем исходный *big.Rat после сохранения
	inputRat.SetInt64(1)

	gotFirst, err := repo.GetRate(ctx, "USD", nil)
	if err != nil {
		t.Fatalf("first GetRate failed: %v", err)
	}
	if gotFirst.RUBPerUnitString() != "90.0000000000" {
		t.Fatalf("stored rate mutated via input pointer: got %s, want 90.0000000000", gotFirst.RUBPerUnitString())
	}

	// 2. Мутируем *big.Rat, полученный из GetRate
	gotFirst.RUBPerUnit.SetInt64(1)

	gotSecond, err := repo.GetRate(ctx, "USD", nil)
	if err != nil {
		t.Fatalf("second GetRate failed: %v", err)
	}
	if gotSecond.RUBPerUnitString() != "90.0000000000" {
		t.Fatalf("stored rate mutated via returned pointer: got %s, want 90.0000000000", gotSecond.RUBPerUnitString())
	}
}
