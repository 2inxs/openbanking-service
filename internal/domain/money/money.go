package money

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidCurrency  = errors.New("currency must be a three-letter uppercase code")
	ErrCurrencyMismatch = errors.New("currencies do not match")
)

// Здесь проверяется формат кода валюты; наличие в поддерживаемом справочнике проверяет приложение.
type Currency string

const RUB Currency = "RUB"

func (c Currency) Validate() error {
	if len(c) != 3 {
		return ErrInvalidCurrency
	}
	for _, b := range []byte(c) {
		if b < 'A' || b > 'Z' {
			return ErrInvalidCurrency
		}
	}
	return nil
}

// Money из литерала структуры или декодирования нужно проверить через Validate.
type Money struct {
	Amount   Amount   `json:"amount"`
	Currency Currency `json:"currency"`
}

func New(amount Amount, currency Currency) (Money, error) {
	m := Money{Amount: amount, Currency: currency}
	if err := m.Validate(); err != nil {
		return Money{}, err
	}
	return m, nil
}

func (m Money) Validate() error { return m.Currency.Validate() }

func (m Money) compatible(other Money) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if err := other.Validate(); err != nil {
		return err
	}
	if m.Currency != other.Currency {
		return fmt.Errorf("%w: %s and %s", ErrCurrencyMismatch, m.Currency, other.Currency)
	}
	return nil
}

func (m Money) Add(other Money) (Money, error) {
	if err := m.compatible(other); err != nil {
		return Money{}, err
	}
	return Money{Amount: m.Amount.Add(other.Amount), Currency: m.Currency}, nil
}

func (m Money) Sub(other Money) (Money, error) {
	if err := m.compatible(other); err != nil {
		return Money{}, err
	}
	return Money{Amount: m.Amount.Sub(other.Amount), Currency: m.Currency}, nil
}

func (m Money) Cmp(other Money) (int, error) {
	if err := m.compatible(other); err != nil {
		return 0, err
	}
	return m.Amount.Cmp(other.Amount), nil
}
