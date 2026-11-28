//go:build integration

package integration_test

import (
	"context"
	"errors"
	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/infrastructure/postgres"
	"github.com/2inxs/openbanking-service/internal/usecase/banksync"
	st "github.com/2inxs/openbanking-service/internal/usecase/statement"
	tr "github.com/2inxs/openbanking-service/internal/usecase/transaction"
	"strings"
	"testing"
)

func TestUnitOfWorkCommitRollbackAndImportStates(t *testing.T) {
	ctx := context.Background()
	tx := repositoryTx(t)
	u, b := seedOwner(t, tx)
	c := seedConnection(t, tx, u, b)
	a := bankAccount(t, tx, c)
	if _, err := postgres.NewAccountRepository(tx).Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	runner := postgres.NewStatementUnitOfWork(tx)
	imports := postgres.NewImportRecordRepository(tx)
	transactions := postgres.NewTransactionRepository(tx)
	attempt := st.ImportRecord{ID: domain.StatementImportID(newID(t)), AccountID: a.ID, FileName: "statement.csv", FileSHA256: strings.Repeat("a", 64), Status: st.ImportPending}
	record := transactionRecord(t, a.ID, 100)
	record.Transaction.Source = domain.SourceStatement
	record.Transaction.StatementImportID = attempt.ID
	injected := errors.New("failure after saving transactions")
	save := func(repos st.Repositories, fail bool) error {
		created, err := repos.Imports.Create(ctx, u, attempt)
		if err != nil {
			return err
		}
		created.Status = st.ImportProcessing
		created, err = repos.Imports.Transition(ctx, u, created, st.ImportPending)
		if err != nil {
			return err
		}
		if _, err := repos.Transactions.Create(ctx, u, record); err != nil {
			return err
		}
		if fail {
			return injected
		}
		created.Status = st.ImportCompleted
		created.ImportedCount = 1
		_, err = repos.Imports.Transition(ctx, u, created, st.ImportProcessing)
		return err
	}
	if err := runner.Within(ctx, func(repos st.Repositories) error { return save(repos, true) }); !errors.Is(err, injected) {
		t.Fatalf("callback error lost: %v", err)
	}
	if _, err := imports.Get(ctx, u, attempt.ID); !errors.Is(err, st.ErrImportNotFound) {
		t.Fatalf("import survived rollback: %v", err)
	}
	if _, err := transactions.GetByExternalID(ctx, u, a.ID, record.Transaction.ExternalTransactionID); !errors.Is(err, tr.ErrNotFound) {
		t.Fatalf("transaction survived rollback: %v", err)
	}
	failed := attempt
	failed.ID = domain.StatementImportID(newID(t))
	failed.Status = st.ImportFailed
	failed.ErrorCount = 1
	failed.ErrorMessage = "validation_failed"
	if _, err := imports.Create(ctx, u, failed); err != nil {
		t.Fatalf("separate failure recording: %v", err)
	}
	if err := runner.Within(ctx, func(repos st.Repositories) error { return save(repos, false) }); err != nil {
		t.Fatal(err)
	}
	completed, err := imports.Get(ctx, u, attempt.ID)
	if err != nil || completed.Status != st.ImportCompleted || completed.ImportedCount != 1 {
		t.Fatalf("completed %+v %v", completed, err)
	}
	if _, err := transactions.GetByExternalID(ctx, u, a.ID, record.Transaction.ExternalTransactionID); err != nil {
		t.Fatal(err)
	}
	if _, err := imports.Get(ctx, domain.UserID(newID(t)), attempt.ID); !errors.Is(err, st.ErrImportNotFound) {
		t.Fatalf("foreign import read: %v", err)
	}
	stale := attempt
	stale.Status = st.ImportProcessing
	if _, err := imports.Transition(ctx, u, stale, st.ImportPending); !errors.Is(err, st.ErrImportConflict) {
		t.Fatalf("stale state accepted: %v", err)
	}
	completed.Status = st.ImportFailed
	completed.ImportedCount = 0
	if _, err := imports.Transition(ctx, u, completed, st.ImportCompleted); !errors.Is(err, domain.ErrInvalidModel) {
		t.Fatalf("terminal transition: %v", err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	canceledAttempt := attempt
	canceledAttempt.ID = domain.StatementImportID(newID(t))
	canceledAttempt.FileSHA256 = strings.Repeat("b", 64)
	err = runner.Within(cancelCtx, func(repos st.Repositories) error {
		if _, err := repos.Imports.Create(cancelCtx, u, canceledAttempt); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled operation committed: %v", err)
	}
	if _, err := imports.Get(ctx, u, canceledAttempt.ID); !errors.Is(err, st.ErrImportNotFound) {
		t.Fatalf("canceled import survived: %v", err)
	}
}

func TestBankSyncUnitOfWorkCommitRollbackAndPanic(t *testing.T) {
	ctx := context.Background()
	tx := repositoryTx(t)
	u, b := seedOwner(t, tx)
	c := seedConnection(t, tx, u, b)
	a := bankAccount(t, tx, c)
	runner := postgres.NewBankSyncUnitOfWork(tx)
	accounts := postgres.NewAccountRepository(tx)
	connections := postgres.NewConnectionRepository(tx)
	transactions := postgres.NewTransactionRepository(tx)
	record := transactionRecord(t, a.ID, 101)
	injected := errors.New("stop after bank data was saved")
	save := func(repos banksync.Repositories) error {
		if _, err := repos.Accounts.Create(ctx, a); err != nil {
			return err
		}
		if _, err := repos.Transactions.Create(ctx, u, record); err != nil {
			return err
		}
		_, err := repos.Connections.UpdateSyncState(ctx, u, c.ID, c.Version, banksync.ConnectionSyncState{
			Status: c.Status, LastSyncError: "sync_failed",
		})
		return err
	}
	checkRollback := func() {
		t.Helper()
		if _, err := accounts.Get(ctx, u, a.ID); !errors.Is(err, domain.ErrAccountNotFound) {
			t.Fatalf("account survived rollback: %v", err)
		}
		if _, err := transactions.GetByExternalID(ctx, u, a.ID, record.Transaction.ExternalTransactionID); !errors.Is(err, tr.ErrNotFound) {
			t.Fatalf("transaction survived rollback: %v", err)
		}
		got, err := connections.Get(ctx, u, c.ID)
		if err != nil || got.Version != c.Version || got.LastSyncError != c.LastSyncError {
			t.Fatalf("connection survived rollback: %+v %v", got, err)
		}
	}
	if err := runner.Within(ctx, func(repos banksync.Repositories) error {
		if err := save(repos); err != nil {
			return err
		}
		return injected
	}); !errors.Is(err, injected) {
		t.Fatalf("callback error lost: %v", err)
	}
	checkRollback()
	func() {
		defer func() {
			if recovered := recover(); recovered != injected {
				t.Fatalf("panic changed or swallowed: %v", recovered)
			}
		}()
		_ = runner.Within(ctx, func(repos banksync.Repositories) error {
			if err := save(repos); err != nil {
				t.Fatal(err)
			}
			panic(injected)
		})
	}()
	checkRollback()
	if err := runner.Within(ctx, save); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.Get(ctx, u, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := transactions.GetByExternalID(ctx, u, a.ID, record.Transaction.ExternalTransactionID); err != nil {
		t.Fatal(err)
	}
	got, err := connections.Get(ctx, u, c.ID)
	if err != nil || got.Version != c.Version+1 || got.LastSyncError != "sync_failed" {
		t.Fatalf("bank data not committed together: %+v %v", got, err)
	}
}
