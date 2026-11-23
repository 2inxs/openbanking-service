package banksync

import (
	"context"
	"errors"

	"github.com/2inxs/openbanking-service/internal/domain"
)

var (
	ErrConnectionNotFound = errors.New("bank connection: not found")
	ErrConnectionConflict = errors.New("bank connection: identity or ownership conflict")
)

type ConnectionRepository interface {
	Create(context.Context, domain.BankConnection) (domain.BankConnection, error)
	Get(context.Context, domain.UserID, domain.BankConnectionID) (domain.BankConnection, error)
	GetByBank(context.Context, domain.UserID, domain.BankID) (domain.BankConnection, error)
}

type AccountRepository interface {
	Create(context.Context, domain.Account) (domain.Account, error)
	// UpsertExternal preserves the local ID and rejects a different owner, currency or assigned identifier.
	UpsertExternal(context.Context, domain.Account) (domain.Account, error)
	// BindExternal attaches an existing account after the caller has matched reliable bank identifiers.
	BindExternal(context.Context, domain.UserID, domain.AccountID, domain.BankConnectionID, string) (domain.Account, error)
	// UpdateBalance ignores snapshots older than the saved bank balance.
	UpdateBalance(context.Context, domain.UserID, domain.AccountID, domain.BalanceSnapshot) error
}
