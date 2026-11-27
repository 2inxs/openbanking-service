package postgres

import (
	"context"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/usecase/banksync"
	"github.com/2inxs/openbanking-service/internal/usecase/statement"
	"github.com/jackc/pgx/v5"
)

type BankSyncUnitOfWork struct{ db txBeginner }
type StatementUnitOfWork struct{ db txBeginner }

var _ banksync.TransactionRunner = (*BankSyncUnitOfWork)(nil)
var _ statement.TransactionRunner = (*StatementUnitOfWork)(nil)

func NewBankSyncUnitOfWork(db txBeginner) *BankSyncUnitOfWork {
	return &BankSyncUnitOfWork{db: db}
}

func NewStatementUnitOfWork(db txBeginner) *StatementUnitOfWork {
	return &StatementUnitOfWork{db: db}
}

func (u *BankSyncUnitOfWork) Within(ctx context.Context, fn func(banksync.Repositories) error) error {
	if fn == nil {
		return domain.ErrInvalidModel
	}
	return withinTransaction(ctx, u.db, func(tx pgx.Tx) error {
		return fn(banksync.Repositories{Accounts: NewAccountRepository(tx), Connections: NewConnectionRepository(tx), Transactions: NewTransactionRepository(tx)})
	})
}

func (u *StatementUnitOfWork) Within(ctx context.Context, fn func(statement.Repositories) error) error {
	if fn == nil {
		return domain.ErrInvalidModel
	}
	return withinTransaction(ctx, u.db, func(tx pgx.Tx) error {
		return fn(statement.Repositories{Transactions: NewTransactionRepository(tx), Imports: NewImportRecordRepository(tx)})
	})
}

func withinTransaction(ctx context.Context, db txBeginner, fn func(pgx.Tx) error) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return repositoryError(err, ErrUnavailable, domain.ErrInvalidModel)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return repositoryError(tx.Commit(ctx), ErrUnavailable, domain.ErrInvalidModel)
}
