package openbanking

import (
	"fmt"

	"github.com/2inxs/openbanking-service/internal/domain/money"
)

const (
	indicatorCredit = "Credit"
	indicatorDebit  = "Debit"
)

// signedMoney переводит сумму банка без знака и creditDebitIndicator в деньги со знаком
func signedMoney(a bankAmount, indicator string) (money.Money, error) {
	amount, err := money.Parse(a.Amount)
	if err != nil {
		return money.Money{}, fmt.Errorf("amount %q: %v", a.Amount, err)
	}
	if amount.Sign() < 0 {
		return money.Money{}, fmt.Errorf("amount %q must be unsigned, direction is in creditDebitIndicator", a.Amount)
	}

	switch indicator {
	case indicatorCredit:
		// сумма остаётся положительной
	case indicatorDebit:
		amount = amount.Negate()
	default:
		return money.Money{}, fmt.Errorf("unknown creditDebitIndicator %q", indicator)
	}
	m, err := money.New(amount, money.Currency(a.Currency))
	if err != nil {
		return money.Money{}, fmt.Errorf("currency %q: %v", a.Currency, err)
	}
	return m, nil
}
