//go:build integration

package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/identifier"
	"github.com/2inxs/openbanking-service/internal/infrastructure/postgres"
	"github.com/2inxs/openbanking-service/internal/usecase/banksync"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func repositoryPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg := databaseConfig(t)
	cfg.MaxConns = 10
	cfg.StatementTimeout = 5 * time.Second
	pool, err := postgres.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func repositoryTx(t *testing.T) pgx.Tx {
	t.Helper()
	tx, err := repositoryPool(t).Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

func newID(t *testing.T) string {
	t.Helper()
	id, err := (identifier.UUIDGenerator{}).NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestConnectionSyncStateUsesVersion(t *testing.T) {
	tx := repositoryTx(t)
	ctx := context.Background()
	u, b := seedOwner(t, tx)
	c := seedConnection(t, tx, u, b)
	repo := postgres.NewConnectionRepository(tx)
	last := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	updated, err := repo.UpdateSyncState(ctx, u, c.ID, c.Version, banksync.ConnectionSyncState{
		Status: domain.ConnectionActive, LastSyncedAt: &last,
	})
	if err != nil || updated.Version != c.Version+1 || updated.LastSyncedAt == nil || !updated.LastSyncedAt.Equal(last) {
		t.Fatalf("successful sync state: %+v, %v", updated, err)
	}
	failure := banksync.ConnectionSyncState{Status: domain.ConnectionError, LastSyncError: "bank_timeout"}
	failed, err := repo.UpdateSyncState(ctx, u, c.ID, updated.Version, failure)
	if err != nil || failed.Version != updated.Version+1 || failed.Status != domain.ConnectionError ||
		failed.LastSyncError != "bank_timeout" || failed.LastSyncedAt == nil || !failed.LastSyncedAt.Equal(last) {
		t.Fatalf("failed sync lost previous success: %+v, %v", failed, err)
	}
	if _, err := repo.UpdateSyncState(ctx, u, c.ID, updated.Version, banksync.ConnectionSyncState{Status: domain.ConnectionActive}); !errors.Is(err, banksync.ErrConnectionConflict) {
		t.Fatalf("stale writer accepted: %v", err)
	}
	got, err := repo.Get(ctx, u, c.ID)
	if err != nil || got.Version != failed.Version || got.LastSyncError != "bank_timeout" || got.Status != domain.ConnectionError {
		t.Fatalf("stale writer changed state: %+v, %v", got, err)
	}
	older := last.Add(-time.Hour)
	recovered, err := repo.UpdateSyncState(ctx, u, c.ID, failed.Version, banksync.ConnectionSyncState{
		Status: domain.ConnectionActive, LastSyncedAt: &older,
	})
	if err != nil || recovered.LastSyncError != "" || !recovered.LastSyncedAt.Equal(last) {
		t.Fatalf("recovery did not clear error or rolled sync time back: %+v, %v", recovered, err)
	}
	if _, err := repo.UpdateSyncState(ctx, domain.UserID(newID(t)), c.ID, recovered.Version, failure); !errors.Is(err, banksync.ErrConnectionNotFound) {
		t.Fatalf("foreign sync state: %v", err)
	}
	if _, err := repo.UpdateSyncState(ctx, u, c.ID, -1, failure); !errors.Is(err, domain.ErrInvalidModel) {
		t.Fatalf("negative version accepted: %v", err)
	}
}

func seedOwner(t *testing.T, db postgres.DBTX) (domain.UserID, domain.BankID) {
	t.Helper()
	u, b := newID(t), newID(t)
	if _, err := db.Exec(context.Background(), `INSERT INTO users(id,email,password_hash) VALUES ($1,$2,'!')`, u, u+"@repository.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), `INSERT INTO banks(id,code,name) VALUES ($1,$2,'Repository test')`, b, b); err != nil {
		t.Fatal(err)
	}
	return domain.UserID(u), domain.BankID(b)
}

func seedConnection(t *testing.T, db postgres.DBTX, u domain.UserID, b domain.BankID) domain.BankConnection {
	t.Helper()
	c, err := postgres.NewConnectionRepository(db).Create(context.Background(), domain.BankConnection{
		ID: domain.BankConnectionID(newID(t)), UserID: u, BankID: b, Status: domain.ConnectionActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func bankAccount(t *testing.T, db postgres.DBTX, c domain.BankConnection) domain.Account {
	t.Helper()
	return domain.Account{
		ID: domain.AccountID(newID(t)), UserID: c.UserID, BankID: c.BankID, ConnectionID: c.ID,
		ExternalAccountID: newID(t), Name: "Bank account", Type: domain.AccountCurrent,
		Status: domain.AccountActive, Currency: money.RUB,
	}
}

func snapshot(t *testing.T, value string, date time.Time) domain.BalanceSnapshot {
	t.Helper()
	amount, err := money.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return domain.BalanceSnapshot{Value: money.Money{Amount: amount, Currency: money.RUB}, AsOf: date}
}

func TestConnectionRepository(t *testing.T) {
	tx := repositoryTx(t)
	ctx := context.Background()
	repo := postgres.NewConnectionRepository(tx)
	for _, status := range []domain.ConnectionStatusID{domain.ConnectionActive, domain.ConnectionError, domain.ConnectionDisconnected, domain.ConnectionPending} {
		u, b := seedOwner(t, tx)
		last := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
		in := domain.BankConnection{ID: domain.BankConnectionID(newID(t)), UserID: u, BankID: b,
			CustomerID: "external-customer", Status: status, LastSyncedAt: &last}
		created, err := repo.Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		got, err := repo.GetByBank(ctx, u, b)
		if err != nil || got.ID != created.ID || got.Status != status || got.CustomerID != in.CustomerID || !got.LastSyncedAt.Equal(last) {
			t.Fatalf("connection round trip: %+v, %v", got, err)
		}
		other := domain.UserID(newID(t))
		if _, err := repo.Get(ctx, other, got.ID); !errors.Is(err, banksync.ErrConnectionNotFound) {
			t.Fatalf("foreign connection: %v", err)
		}
		if _, err := repo.GetByBank(ctx, other, b); !errors.Is(err, banksync.ErrConnectionNotFound) {
			t.Fatalf("foreign bank connection: %v", err)
		}
	}
}

func TestAccountRepositoryIsolationAndMappings(t *testing.T) {
	tx := repositoryTx(t)
	ctx := context.Background()
	u, b := seedOwner(t, tx)
	c := seedConnection(t, tx, u, b)
	otherUser, otherBank := seedOwner(t, tx)
	repo := postgres.NewAccountRepository(tx)
	types := []domain.AccountTypeID{domain.AccountCurrent, domain.AccountCreditCard, domain.AccountSavings, domain.AccountDeposit, domain.AccountTypeUnknown}
	statuses := []domain.AccountStatusID{domain.AccountActive, domain.AccountClosed, domain.AccountStatusUnknown}
	for _, typ := range types {
		for _, status := range statuses {
			a := bankAccount(t, tx, c)
			a.Type, a.Status = typ, status
			created, err := repo.Create(ctx, a)
			if err != nil {
				t.Fatal(err)
			}
			got, err := repo.Get(ctx, u, created.ID)
			if err != nil || got.Type != typ || got.Status != status || got.Balance != nil || got.Number != "" {
				t.Fatalf("account round trip: %+v, %v", got, err)
			}
			if _, err := repo.Get(ctx, otherUser, created.ID); !errors.Is(err, domain.ErrAccountNotFound) {
				t.Fatalf("foreign account: %v", err)
			}
		}
	}
	accounts, err := repo.ListByUser(ctx, u)
	if err != nil || len(accounts) != len(types)*len(statuses) {
		t.Fatalf("list: %d, %v", len(accounts), err)
	}
	for i := 1; i < len(accounts); i++ {
		if accounts[i-1].ID >= accounts[i].ID {
			t.Fatal("unstable account ordering")
		}
	}
	accounts, err = repo.ListByUser(ctx, otherUser)
	if err != nil || len(accounts) != 0 {
		t.Fatalf("foreign list: %v, %v", accounts, err)
	}
	// Expected SQL errors need savepoints so the surrounding test transaction remains usable.
	for _, change := range []func(*domain.Account){
		func(a *domain.Account) { a.UserID = otherUser },
		func(a *domain.Account) { a.BankID = otherBank },
	} {
		sub, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		a := bankAccount(t, sub, c)
		change(&a)
		_, err = postgres.NewAccountRepository(sub).Create(ctx, a)
		_ = sub.Rollback(ctx)
		if !errors.Is(err, domain.ErrAccountConflict) {
			t.Fatalf("foreign binding accepted: %v", err)
		}
	}
}

func TestAccountRediscoveryAndBalance(t *testing.T) {
	tx := repositoryTx(t)
	ctx := context.Background()
	u, b := seedOwner(t, tx)
	c := seedConnection(t, tx, u, b)
	repo := postgres.NewAccountRepository(tx)
	a := bankAccount(t, tx, c)
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	balance := snapshot(t, "-1234567890123456.7891", now)
	a.Balance = &balance
	first, err := repo.UpsertExternal(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	a.ID = domain.AccountID(newID(t))
	a.Name, a.Number = "New bank name", "40817810000000000001"
	old := snapshot(t, "5", now.Add(-time.Hour))
	a.Balance = &old
	second, err := repo.UpsertExternal(ctx, a)
	if err != nil || second.ID != first.ID || second.Name != a.Name || second.Number != a.Number || second.Balance.Value.Amount.String() != balance.Value.Amount.String() {
		t.Fatalf("rediscovery: %+v, %v", second, err)
	}
	a.Number, a.Balance = "", nil
	second, err = repo.UpsertExternal(ctx, a)
	if err != nil || second.Balance == nil || second.Number == "" {
		t.Fatalf("missing data erased account: %+v, %v", second, err)
	}
	list, err := repo.ListByUser(ctx, u)
	if err != nil || len(list) != 1 {
		t.Fatalf("duplicate: %d, %v", len(list), err)
	}
	for _, change := range []func(*domain.Account){
		func(a *domain.Account) { a.Currency = "USD" },
		func(a *domain.Account) { a.Number = "another-number" },
		func(a *domain.Account) { a.UserID = domain.UserID(newID(t)) },
		func(a *domain.Account) { a.ConnectionID = domain.BankConnectionID(newID(t)) },
	} {
		in := a
		change(&in)
		if _, err := repo.UpsertExternal(ctx, in); !errors.Is(err, domain.ErrAccountConflict) {
			t.Fatalf("changed identity accepted: %v", err)
		}
	}
	fresh := snapshot(t, "42.1234", now.Add(time.Hour))
	if err := repo.UpdateBalance(ctx, u, first.ID, fresh); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateBalance(ctx, u, first.ID, old); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, u, first.ID)
	if err != nil || got.Balance.Value.Amount.String() != "42.1234" || !got.Balance.AsOf.Equal(fresh.AsOf) {
		t.Fatalf("stale balance won: %+v, %v", got, err)
	}
	fresh.Value.Currency = "USD"
	if err := repo.UpdateBalance(ctx, u, first.ID, fresh); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("currency mismatch: %v", err)
	}
	if err := repo.UpdateBalance(ctx, domain.UserID(newID(t)), first.ID, old); !errors.Is(err, domain.ErrAccountNotFound) {
		t.Fatalf("foreign balance: %v", err)
	}
	if err := repo.UpdateBalance(ctx, u, first.ID, snapshot(t, "10000000000000000", now)); !errors.Is(err, domain.ErrInvalidModel) {
		t.Fatalf("overflow: %v", err)
	}
}

func TestBindStatementAccount(t *testing.T) {
	tx := repositoryTx(t)
	ctx := context.Background()
	u, b := seedOwner(t, tx)
	c := seedConnection(t, tx, u, b)
	otherUser, otherBank := seedOwner(t, tx)
	foreign := seedConnection(t, tx, otherUser, otherBank)
	repo := postgres.NewAccountRepository(tx)
	a := bankAccount(t, tx, c)
	a.ConnectionID, a.ExternalAccountID, a.Number = "", "", "40817810000000000002"
	created, err := repo.Create(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if created.ConnectionID != "" {
		t.Fatal("statement account unexpectedly connected")
	}
	if _, err := repo.BindExternal(ctx, u, a.ID, foreign.ID, "external"); !errors.Is(err, domain.ErrAccountConflict) {
		t.Fatalf("foreign connection: %v", err)
	}
	if _, err := repo.BindExternal(ctx, otherUser, a.ID, c.ID, "external"); !errors.Is(err, domain.ErrAccountNotFound) {
		t.Fatalf("foreign owner: %v", err)
	}
	bound, err := repo.BindExternal(ctx, u, a.ID, c.ID, "external")
	if err != nil || bound.ID != a.ID || bound.ConnectionID != c.ID || bound.ExternalAccountID != "external" {
		t.Fatalf("bind: %+v, %v", bound, err)
	}
	if _, err := repo.BindExternal(ctx, u, a.ID, c.ID, "external"); err != nil {
		t.Fatalf("repeat binding: %v", err)
	}
	if _, err := repo.BindExternal(ctx, u, a.ID, c.ID, "replacement"); !errors.Is(err, domain.ErrAccountConflict) {
		t.Fatalf("replaced external ID: %v", err)
	}
}

func TestConcurrentAccountDiscovery(t *testing.T) {
	pool := repositoryPool(t)
	ctx := context.Background()
	u, b := seedOwner(t, pool)
	t.Cleanup(func() {
		// Only delete rows belonging to this test's freshly generated owner and bank.
		for _, q := range []string{`DELETE FROM accounts WHERE user_id=$1`, `DELETE FROM bank_connections WHERE user_id=$1`, `DELETE FROM users WHERE id=$1`} {
			if _, err := pool.Exec(context.Background(), q, string(u)); err != nil {
				t.Error(err)
			}
		}
		if _, err := pool.Exec(context.Background(), `DELETE FROM banks WHERE id=$1`, string(b)); err != nil {
			t.Error(err)
		}
	})
	c := seedConnection(t, pool, u, b)
	repo := postgres.NewAccountRepository(pool)
	a := bankAccount(t, pool, c)
	const n = 8
	inputs := make([]domain.Account, n)
	for i := range inputs {
		inputs[i] = a
		inputs[i].ID = domain.AccountID(newID(t))
		v := snapshot(t, "100.1234", time.Date(2026, 11, 23, i, 0, 0, 0, time.UTC))
		inputs[i].Balance = &v
	}
	var wg sync.WaitGroup
	results := make(chan domain.Account, n)
	errs := make(chan error, n)
	for _, in := range inputs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := repo.UpsertExternal(ctx, in)
			if err != nil {
				errs <- err
				return
			}
			results <- got
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var id domain.AccountID
	for got := range results {
		if id != "" && id != got.ID {
			t.Fatal("concurrent discovery created multiple accounts")
		}
		id = got.ID
	}
	list, err := repo.ListByUser(ctx, u)
	if err != nil || len(list) != 1 || !list[0].Balance.AsOf.Equal(inputs[n-1].Balance.AsOf) {
		t.Fatalf("concurrent result: %+v, %v", list, err)
	}
}
