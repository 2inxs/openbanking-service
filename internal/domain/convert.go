package domain

import (
	"fmt"
	"math/big"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain/money"
)

// ConversionResult содержит результат точной конвертации суммы через RUB.
type ConversionResult struct {
	Source            money.Money
	Target            money.Money
	EffectiveRate     *big.Rat
	RateDate          time.Time
	FromRateDate      time.Time
	ToRateDate        time.Time
	ZeroAfterRounding bool
}

// EffectiveRateString форматирует итоговый кросс-курс с точностью ExchangeRateScale (10 знаков).
func (r ConversionResult) EffectiveRateString() string {
	if r.EffectiveRate == nil {
		return "0.0000000000"
	}
	return r.EffectiveRate.FloatString(ExchangeRateScale)
}

// ConvertViaRUB выполняет точную конвертацию src из валюты fromRate в валюту toRate через RUB
// без промежуточного округления и округляет итог один раз до places знаков (0..4).
func ConvertViaRUB(src money.Money, fromRate, toRate ExchangeRate, places int) (ConversionResult, error) {
	if err := src.Validate(); err != nil {
		return ConversionResult{}, fmt.Errorf("convert.source: %w", err)
	}
	if err := fromRate.Validate(); err != nil {
		return ConversionResult{}, fmt.Errorf("convert.from_rate: %w", err)
	}
	if err := toRate.Validate(); err != nil {
		return ConversionResult{}, fmt.Errorf("convert.to_rate: %w", err)
	}
	if src.Currency != fromRate.Currency {
		return ConversionResult{}, fmt.Errorf("convert.from_rate: %w: source is %s, rate is %s", money.ErrCurrencyMismatch, src.Currency, fromRate.Currency)
	}
	if places < 0 || places > money.Scale {
		return ConversionResult{}, money.ErrInvalidScale
	}

	effectiveDate := fromRate.RateDate
	if !toRate.RateDate.IsZero() && (effectiveDate.IsZero() || toRate.RateDate.Before(effectiveDate)) {
		effectiveDate = toRate.RateDate
	}

	if src.Currency == toRate.Currency {
		rounded, err := src.Amount.Round(places)
		if err != nil {
			return ConversionResult{}, err
		}
		return ConversionResult{
			Source:            src,
			Target:            money.Money{Amount: rounded, Currency: toRate.Currency},
			EffectiveRate:     big.NewRat(1, 1),
			RateDate:          effectiveDate,
			FromRateDate:      fromRate.RateDate,
			ToRateDate:        toRate.RateDate,
			ZeroAfterRounding: src.Amount.Sign() != 0 && rounded.Sign() == 0,
		}, nil
	}

	// crossRate = RUBPerUnit(from) / RUBPerUnit(to)
	crossRate := new(big.Rat).Quo(new(big.Rat).Set(fromRate.RUBPerUnit), toRate.RUBPerUnit)
	// targetRat = src.Amount * crossRate (без промежуточного округления в RUB)
	targetRat := new(big.Rat).Mul(src.Amount.Rat(), crossRate)

	targetAmount, err := money.FromRat(targetRat, places)
	if err != nil {
		return ConversionResult{}, err
	}

	return ConversionResult{
		Source:            src,
		Target:            money.Money{Amount: targetAmount, Currency: toRate.Currency},
		EffectiveRate:     crossRate,
		RateDate:          effectiveDate,
		FromRateDate:      fromRate.RateDate,
		ToRateDate:        toRate.RateDate,
		ZeroAfterRounding: src.Amount.Sign() != 0 && targetAmount.Sign() == 0,
	}, nil
}
