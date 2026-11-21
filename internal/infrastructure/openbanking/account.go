package openbanking

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/usecase/banksync"
)

var ErrInvalidBankData = errors.New("invalid bank data")

const schemeCBRAccountNumber = "RU.CBR.AccountNumber"

func normalizeAccount(acc bankAccount) (banksync.BankAccount, error) {
	if acc.AccountID == "" {
		return banksync.BankAccount{}, fmt.Errorf("%w: account: empty accountId", ErrInvalidBankData)
	}
	err := money.Currency(acc.Currency).Validate()
	if err != nil {
		return banksync.BankAccount{}, fmt.Errorf("%w: account %s: currency %q: %v", ErrInvalidBankData, acc.AccountID, acc.Currency, err)
	}
	var number string
	for _, d := range acc.AccountDetails {
		if d.SchemeName == schemeCBRAccountNumber {
			number = d.Identification
			break
		}
	}
	if number == "" {
		return banksync.BankAccount{}, fmt.Errorf("%w: account %s: no %s identification",
			ErrInvalidBankData, acc.AccountID, schemeCBRAccountNumber)
	}
	name := acc.AccountDescription
	if name == "" {
		name = number
	}
	return banksync.BankAccount{
		ExternalID: acc.AccountID,
		Number:     number,
		Name:       name,
		Type:       accountType(acc.AccountSubType),
		Status:     accountStatus(acc.Status),
		Currency:   money.Currency(acc.Currency),
	}, nil
}

func accountType(subType string) domain.AccountTypeID {
	switch subType {
	case "CurrentAccount":
		return domain.AccountCurrent
	case "CreditCard":
		return domain.AccountCreditCard
	case "Savings":
		return domain.AccountSavings
	default:
		return domain.AccountTypeUnknown
	}
}

func accountStatus(status string) domain.AccountStatusID {
	switch status {
	case "Enabled":
		return domain.AccountActive
	case "Deleted":
		return domain.AccountClosed
	default: // Disabled, Pending и всё неизвестное
		return domain.AccountStatusUnknown
	}
}

func ParseAccounts(body []byte) ([]banksync.BankAccount, error) {
	var resp accountsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("%w: accounts response: %v", ErrInvalidBankData, err)
	}

	accounts := make([]banksync.BankAccount, 0, len(resp.Data.Account))
	for _, acc := range resp.Data.Account {
		normalized, err := normalizeAccount(acc)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, normalized)
	}
	return accounts, nil
}
