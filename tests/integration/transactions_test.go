//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/postgres"
	tr "github.com/2inxs/openbanking-service/internal/usecase/transaction"
	"testing"
	"time"
)

func transactionRecord(t *testing.T, a domain.AccountID, n int) tr.Record {
	t.Helper()
	amount, _ := money.Parse("-123456789.1234")
	day := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	return tr.Record{Transaction: domain.Transaction{ID: domain.TransactionID(newID(t)), AccountID: a, ExternalTransactionID: fmt.Sprintf("operation-%d", n), Source: domain.SourceOpenBanking, Status: domain.TransactionBooked, Value: money.Money{Amount: amount, Currency: "RUB"}, BookedAt: &day}, OccurredAt: day, DeduplicationKey: fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprint(n))))}
}
func TestTransactionRepositoryIsolationPaginationAndConflicts(t *testing.T) {
	ctx := context.Background()
	tx := repositoryTx(t)
	u, b := seedOwner(t, tx)
	c := seedConnection(t, tx, u, b)
	a := bankAccount(t, tx, c)
	if _, err := postgres.NewAccountRepository(tx).Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewTransactionRepository(tx)
	empty, err := repo.List(ctx, u, tr.Filter{AccountID: a.ID})
	if err != nil || empty.Items == nil || len(empty.Items) != 0 || empty.Next != nil {
		t.Fatalf("empty owned account: %+v %v", empty, err)
	}
	for _, tc := range []struct {
		name    string
		user    domain.UserID
		account domain.AccountID
	}{
		{"missing", u, domain.AccountID(newID(t))},
		{"foreign", domain.UserID(newID(t)), a.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := repo.List(ctx, tc.user, tr.Filter{AccountID: tc.account}); !errors.Is(err, tr.ErrNotFound) {
				t.Fatalf("unavailable account returned an empty list: %v", err)
			}
		})
	}
	first := transactionRecord(t, a.ID, 1)
	for n := 1; n <= 3; n++ {
		if _, err := repo.Create(ctx, u, transactionRecord(t, a.ID, n)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.GetByExternalID(ctx, u, a.ID, first.Transaction.ExternalTransactionID)
	if err != nil || got.Transaction.Value.Amount.String() != "-123456789.1234" {
		t.Fatalf("round trip %+v %v", got, err)
	}
	if _, err := repo.GetByDeduplicationKey(ctx, u, a.ID, first.DeduplicationKey); err != nil {
		t.Fatal(err)
	}
	other := domain.UserID(newID(t))
	if _, err := repo.GetByExternalID(ctx, other, a.ID, first.Transaction.ExternalTransactionID); !errors.Is(err, tr.ErrNotFound) {
		t.Fatalf("foreign read: %v", err)
	}
	if _, err := repo.Create(ctx, other, transactionRecord(t, a.ID, 4)); !errors.Is(err, tr.ErrNotFound) {
		t.Fatalf("foreign write: %v", err)
	}
	page, err := repo.List(ctx, u, tr.Filter{AccountID: a.ID, Limit: 2})
	if err != nil || len(page.Items) != 2 || page.Next == nil {
		t.Fatalf("page %+v %v", page, err)
	}
	next, err := repo.List(ctx, u, tr.Filter{AccountID: a.ID, Limit: 2, After: page.Next})
	if err != nil || len(next.Items) != 1 || next.Next != nil {
		t.Fatalf("next %+v %v", next, err)
	}
	if next.Items[0].Transaction.ID >= page.Items[1].Transaction.ID {
		t.Fatal("unstable tie ordering")
	}
	sub, _ := tx.Begin(ctx)
	_, err = postgres.NewTransactionRepository(sub).Create(ctx, u, first)
	_ = sub.Rollback(ctx)
	if !errors.Is(err, tr.ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	sub, _ = tx.Begin(ctx)
	bad := transactionRecord(t, a.ID, 5)
	bad.Transaction.Value.Currency = "USD"
	_, err = postgres.NewTransactionRepository(sub).Create(ctx, u, bad)
	_ = sub.Rollback(ctx)
	if !errors.Is(err, domain.ErrInvalidModel) || errors.Is(err, tr.ErrConflict) {
		t.Fatalf("FK error hidden as duplicate: %v", err)
	}
	from := first.OccurredAt.Add(time.Hour)
	empty, err = repo.List(ctx, u, tr.Filter{AccountID: a.ID, From: &from})
	if err != nil || len(empty.Items) != 0 {
		t.Fatalf("period %+v %v", empty, err)
	}
}
