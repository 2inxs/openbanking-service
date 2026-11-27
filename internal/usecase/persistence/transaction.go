package persistence

import (
	"context"
	"github.com/2inxs/openbanking-service/internal/usecase/account"
	"github.com/2inxs/openbanking-service/internal/usecase/banksync"
	"github.com/2inxs/openbanking-service/internal/usecase/statement"
	"github.com/2inxs/openbanking-service/internal/usecase/transaction"
)

type Accounts interface {
	account.Repository
	banksync.AccountRepository
}
type Repositories struct {
	Accounts     Accounts
	Connections  banksync.ConnectionRepository
	Transactions transaction.Repository
	Imports      statement.ImportRecords
}

// TransactionRunner is for persistence only: finish HTTP requests and file reads before calling Within.
// Repositories passed to fn share one transaction and must not escape the callback.
// If fn fails, record a failed import separately after Within returns (the attempt inside was rolled back).
type TransactionRunner interface {
	Within(context.Context, func(Repositories) error) error
}
