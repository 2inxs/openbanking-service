package openbanking

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
)

const balanceTypeClosingAvailable = "ClosingAvailable"

func normalizeBalance(b bankBalance, accountID string) (domain.BalanceSnapshot, error) {
	if b.AccountID != accountID {
		return domain.BalanceSnapshot{}, fmt.Errorf("%w: balance: accountId %q, requested %q", ErrInvalidBankData, b.AccountID, accountID)
	}
	value, err := signedMoney(b.Amount, b.CreditDebitIndicator)
	if err != nil {
		return domain.BalanceSnapshot{}, fmt.Errorf("%w: balance of account %s: %v", ErrInvalidBankData, accountID, err)
	}
	asOf, err := time.Parse(time.RFC3339, b.DateTime)
	if err != nil {
		return domain.BalanceSnapshot{}, fmt.Errorf("%w: balance of account %s: dateTime %q: %v", ErrInvalidBankData, accountID, b.DateTime, err)
	}
	return domain.BalanceSnapshot{
		Value: value,
		AsOf:  asOf,
	}, nil
}

func ParseBalance(data []byte, accountID string) (domain.BalanceSnapshot, error) {
	if accountID == "" {
		return domain.BalanceSnapshot{}, fmt.Errorf("%w: empty requested accountId", ErrInvalidBankData)
	}
	var resp balancesResponse
	err := json.Unmarshal(data, &resp)
	if err != nil {
		return domain.BalanceSnapshot{}, fmt.Errorf("%w: balances response: %v", ErrInvalidBankData, err)
	}
	for _, balance := range resp.Data.Balance {
		if balance.Type == balanceTypeClosingAvailable {
			return normalizeBalance(balance, accountID)
		}
	}
	return domain.BalanceSnapshot{}, fmt.Errorf("%w: account %s: no %s balance", ErrInvalidBankData, accountID, balanceTypeClosingAvailable)
}
