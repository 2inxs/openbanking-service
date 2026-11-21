package domain

import (
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain/money"
)

const ExchangeRateScale = 10

var ErrUnknownCurrency = errors.New("exchange_rate: unknown currency")

// ExchangeRate представляет нормализованный курс 1 единицы валюты в рублях (RUB)
// на фактическую дату действия.
type ExchangeRate struct {
	Currency   money.Currency
	RateDate   time.Time
	RUBPerUnit *big.Rat
}

func (r ExchangeRate) Validate() error {
	if err := r.Currency.Validate(); err != nil {
		return fmt.Errorf("exchange_rate.currency: %w", err)
	}
	if r.RateDate.IsZero() {
		return invalid("exchange_rate.rate_date", "must be a real date")
	}
	if r.RUBPerUnit == nil || r.RUBPerUnit.Sign() <= 0 {
		return invalid("exchange_rate.rub_per_unit", "must be positive")
	}
	return nil
}

func (r ExchangeRate) RUBPerUnitString() string {
	if r.RUBPerUnit == nil {
		return "0.0000000000"
	}
	return r.RUBPerUnit.FloatString(ExchangeRateScale)
}

// DailyRates содержит набор курсов на фактическую дату их действия.
type DailyRates struct {
	Date  time.Time
	Rates map[money.Currency]ExchangeRate
}

// RateFor возвращает нормализованный курс для указанной валюты.
func (d DailyRates) RateFor(currency money.Currency) (ExchangeRate, error) {
	if err := currency.Validate(); err != nil {
		return ExchangeRate{}, fmt.Errorf("%w %q: %v", ErrUnknownCurrency, currency, err)
	}
	if currency == money.RUB {
		return ExchangeRate{
			Currency:   money.RUB,
			RateDate:   d.Date,
			RUBPerUnit: big.NewRat(1, 1),
		}, nil
	}
	rate, ok := d.Rates[currency]
	if !ok || rate.RUBPerUnit == nil || rate.RUBPerUnit.Sign() <= 0 {
		return ExchangeRate{}, fmt.Errorf("%w: %s", ErrUnknownCurrency, currency)
	}
	return rate, nil
}
