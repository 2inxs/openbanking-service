package statement

import (
	"context"

	"github.com/2inxs/openbanking-service/internal/usecase/transaction"
)

type Repositories struct {
	Transactions transaction.Repository
	Imports      ImportRecords
}

// TransactionRunner saves prepared statement data atomically; read the file before Within.
// Repositories are valid only inside the callback. Record failures separately after rollback.
type TransactionRunner interface {
	Within(context.Context, func(Repositories) error) error
}
