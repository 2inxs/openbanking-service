package money_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/2inxs/openbanking-service/internal/domain/money"
)

func TestMoneyArithmeticRequiresSameCurrency(t *testing.T) {
	rub, err := money.New(amount(t, "100.25"), money.RUB)
	if err != nil {
		t.Fatal(err)
	}
	fee, err := money.New(amount(t, "-0.25"), money.RUB)
	if err != nil {
		t.Fatal(err)
	}
	got, err := rub.Add(fee)
	if err != nil || got.Amount.String() != "100.0000" || got.Currency != money.RUB {
		t.Fatalf("unexpected result: %+v, %v", got, err)
	}
	difference, err := rub.Sub(fee)
	if err != nil || difference.Amount.String() != "100.5000" {
		t.Fatalf("unexpected subtraction: %+v, %v", difference, err)
	}
	if cmp, err := rub.Cmp(fee); err != nil || cmp <= 0 {
		t.Fatalf("unexpected comparison: %d, %v", cmp, err)
	}
	eur, err := money.New(amount(t, "1"), "EUR")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rub.Add(eur); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("mixed currencies: %v", err)
	}
	if _, err := rub.Sub(eur); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("mixed currencies: %v", err)
	}
	if _, err := rub.Cmp(eur); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("mixed currencies: %v", err)
	}
	if _, err := rub.Add(money.Money{}); !errors.Is(err, money.ErrInvalidCurrency) {
		t.Fatalf("invalid zero money: %v", err)
	}
}

func TestCurrencyFormatAndMoneyJSON(t *testing.T) {
	for _, code := range []money.Currency{"", "rub", "RU", "RUB ", "123", "РУБ"} {
		if _, err := money.New(money.Amount{}, code); !errors.Is(err, money.ErrInvalidCurrency) {
			t.Fatalf("accepted invalid currency %q: %v", code, err)
		}
	}
	m, err := money.New(amount(t, "12.3"), "EUR")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(m)
	if err != nil || string(data) != `{"amount":"12.3000","currency":"EUR"}` {
		t.Fatalf("unexpected JSON: %s, %v", data, err)
	}
}
