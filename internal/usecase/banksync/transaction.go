package banksync

import (
	"context"

	"github.com/2inxs/openbanking-service/internal/usecase/transaction"
)

type Repositories struct {
	Accounts     AccountRepository
	Connections  ConnectionRepository
	Transactions transaction.Repository
}

// TransactionRunner saves prepared bank data atomically; fetch bank responses before Within.
// Repositories are valid only inside the callback and must not be used concurrently.
type TransactionRunner interface {
	Within(context.Context, func(Repositories) error) error
}
