package banksync

import (
	"context"
	"errors"
	"time"

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
	// UpdateSyncState requires the version returned by Get; a stale version is a conflict.
	UpdateSyncState(context.Context, domain.UserID, domain.BankConnectionID, int64, ConnectionSyncState) (domain.BankConnection, error)
}

type IDGenerator interface {
	NewID() (string, error)
}

type ConnectionSyncState struct {
	Status domain.ConnectionStatusID
	// nil preserves the last successful sync time; an older timestamp is ignored.
	LastSyncedAt *time.Time
	// Use a safe message or code, never a raw bank response or credential.
	LastSyncError string
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
