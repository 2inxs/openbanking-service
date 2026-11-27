package postgres

import (
	"context"
	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/usecase/persistence"
	"time"
)

type UnitOfWork struct{ db txBeginner }

var _ persistence.TransactionRunner = (*UnitOfWork)(nil)

func NewUnitOfWork(db txBeginner) *UnitOfWork { return &UnitOfWork{db: db} }
func (u *UnitOfWork) Within(ctx context.Context, fn func(persistence.Repositories) error) error {
	if fn == nil {
		return domain.ErrInvalidModel
	}
	tx, err := u.db.Begin(ctx)
	if err != nil {
		return repositoryError(err, ErrUnavailable, domain.ErrInvalidModel)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	repos := persistence.Repositories{Accounts: NewAccountRepository(tx), Connections: NewConnectionRepository(tx), Transactions: NewTransactionRepository(tx), Imports: NewImportRecordRepository(tx)}
	if err := fn(repos); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return repositoryError(tx.Commit(ctx), ErrUnavailable, domain.ErrInvalidModel)
}
