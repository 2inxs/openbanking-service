package transaction_test

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/usecase/transaction"
)

type stubAccountReader struct {
	accounts map[domain.AccountID]domain.Account
	err      error
	calls    int
	bypass   bool
}

func (r *stubAccountReader) Get(_ context.Context, userID domain.UserID, id domain.AccountID) (domain.Account, error) {
	r.calls++
	if r.err != nil {
		return domain.Account{}, r.err
	}
	acc, ok := r.accounts[id]
	if !ok {
		return domain.Account{}, domain.ErrAccountNotFound
	}
	if !r.bypass && acc.UserID != userID {
		return domain.Account{}, domain.ErrAccountNotFound
	}
	return acc, nil
}

type stubTransactionRepo struct {
	records []transaction.Record
	err     error
	calls   int
	last    transaction.Filter
}

func (r *stubTransactionRepo) Create(context.Context, domain.UserID, transaction.Record) (transaction.Record, error) {
	return transaction.Record{}, nil
}

func (r *stubTransactionRepo) GetByExternalID(context.Context, domain.UserID, domain.AccountID, string) (transaction.Record, error) {
	return transaction.Record{}, transaction.ErrNotFound
}

func (r *stubTransactionRepo) GetByDeduplicationKey(context.Context, domain.UserID, domain.AccountID, string) (transaction.Record, error) {
	return transaction.Record{}, transaction.ErrNotFound
}

func (r *stubTransactionRepo) List(_ context.Context, _ domain.UserID, f transaction.Filter) (transaction.Page, error) {
	r.calls++
	r.last = f
	if r.err != nil {
		return transaction.Page{}, r.err
	}

	filtered := make([]transaction.Record, 0, len(r.records))
	for _, rec := range r.records {
		if rec.Transaction.AccountID != f.AccountID {
			continue
		}
		if f.From != nil && rec.OccurredAt.Before(*f.From) {
			continue
		}
		if f.To != nil && !rec.OccurredAt.Before(*f.To) {
			continue
		}
		filtered = append(filtered, rec)
	}

	sort.Slice(filtered, func(i, j int) bool {
		if !filtered[i].OccurredAt.Equal(filtered[j].OccurredAt) {
			return filtered[i].OccurredAt.After(filtered[j].OccurredAt)
		}
		return filtered[i].Transaction.ID > filtered[j].Transaction.ID
	})

	start := 0
	if f.After != nil {
		start = len(filtered)
		for i, rec := range filtered {
			if rec.OccurredAt.Before(f.After.OccurredAt) ||
				(rec.OccurredAt.Equal(f.After.OccurredAt) && rec.Transaction.ID < f.After.ID) {
				start = i
				break
			}
		}
	}

	if start >= len(filtered) {
		return transaction.Page{Items: []transaction.Record{}}, nil
	}

	end := start + f.Limit
	var next *transaction.Cursor
	if end < len(filtered) {
		lastItem := filtered[end-1]
		next = &transaction.Cursor{
			OccurredAt: lastItem.OccurredAt,
			ID:         lastItem.Transaction.ID,
		}
	} else {
		end = len(filtered)
	}

	return transaction.Page{
		Items: filtered[start:end],
		Next:  next,
	}, nil
}

func mustAmount(t *testing.T, raw string) money.Amount {
	t.Helper()
	a, err := money.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestListAccountTransactions_PaginationPeriodAndAccess(t *testing.T) {
	ctx := context.Background()
	user1 := domain.UserID("user-1")
	user2 := domain.UserID("user-2")
	acc1 := domain.AccountID("acc-1")
	accEmpty := domain.AccountID("acc-empty")
	accForeign := domain.AccountID("acc-foreign")

	accRepo := &stubAccountReader{
		accounts: map[domain.AccountID]domain.Account{
			acc1: {
				ID:       acc1,
				UserID:   user1,
				BankID:   "bank-1",
				Number:   "40817810000000000001",
				Name:     "Основной",
				Type:     domain.AccountCurrent,
				Status:   domain.AccountActive,
				Currency: money.RUB,
			},
			accEmpty: {
				ID:       accEmpty,
				UserID:   user1,
				BankID:   "bank-1",
				Number:   "40817810000000000002",
				Name:     "Пустой",
				Type:     domain.AccountCurrent,
				Status:   domain.AccountActive,
				Currency: money.RUB,
			},
			accForeign: {
				ID:       accForeign,
				UserID:   user2,
				BankID:   "bank-1",
				Number:   "40817810000000009999",
				Name:     "Чужой",
				Type:     domain.AccountCurrent,
				Status:   domain.AccountActive,
				Currency: money.RUB,
			},
		},
	}

	t1 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	tSame := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 11, 22, 15, 0, 0, 0, time.UTC)

	makeRec := func(id string, at time.Time, amt string) transaction.Record {
		b := at
		return transaction.Record{
			OccurredAt: at,
			Transaction: domain.Transaction{
				ID:          domain.TransactionID(id),
				AccountID:   acc1,
				Source:      domain.SourceOpenBanking,
				Status:      domain.TransactionBooked,
				Value:       money.Money{Amount: mustAmount(t, amt), Currency: money.RUB},
				BookedAt:    &b,
				Description: "Операция " + id,
			},
		}
	}

	txRepo := &stubTransactionRepo{
		records: []transaction.Record{
			makeRec("tx-1", t1, "-100.00"),
			makeRec("tx-2a", tSame, "-250.00"),
			makeRec("tx-2b", tSame, "-300.00"),
			makeRec("tx-3", t3, "1500.00"),
		},
	}

	svc := transaction.NewService(accRepo, txRepo)

	// 1. Стабильная пагинация без потери записей с одинаковым OccurredAt (tSame).
	var collected []domain.TransactionID
	var cursor *transaction.Cursor
	for {
		res, err := svc.ListAccountTransactions(ctx, transaction.ListQuery{
			UserID:    user1,
			AccountID: acc1,
			After:     cursor,
			Limit:     2,
		})
		if err != nil {
			t.Fatalf("ListAccountTransactions failed: %v", err)
		}
		for _, item := range res.Items {
			collected = append(collected, item.ID)
		}
		if res.Next == nil {
			break
		}
		cursor = res.Next
	}
	wantOrder := []domain.TransactionID{"tx-3", "tx-2b", "tx-2a", "tx-1"}
	if len(collected) != len(wantOrder) {
		t.Fatalf("expected %d transactions, got %v", len(wantOrder), collected)
	}
	for i := range wantOrder {
		if collected[i] != wantOrder[i] {
			t.Fatalf("at index %d: want %s, got %s", i, wantOrder[i], collected[i])
		}
	}

	// 2. Границы периода: From включительно, To исключительно [tSame, t3).
	periodRes, err := svc.ListAccountTransactions(ctx, transaction.ListQuery{
		UserID:    user1,
		AccountID: acc1,
		From:      &tSame,
		To:        &t3,
	})
	if err != nil {
		t.Fatalf("period query failed: %v", err)
	}
	if len(periodRes.Items) != 2 || periodRes.Items[0].ID != "tx-2b" || periodRes.Items[1].ID != "tx-2a" {
		t.Fatalf("unexpected period items: %+v", periodRes.Items)
	}

	// 3. Пустой результат возвращает ненулевой пустой срез и Next == nil.
	emptyRes, err := svc.ListAccountTransactions(ctx, transaction.ListQuery{
		UserID:    user1,
		AccountID: accEmpty,
	})
	if err != nil {
		t.Fatalf("empty account query failed: %v", err)
	}
	if emptyRes.Items == nil || len(emptyRes.Items) != 0 || emptyRes.Next != nil {
		t.Fatalf("expected empty non-nil slice and nil Next, got %+v", emptyRes)
	}

	// 4. Чужой счёт -> ErrAccountNotFound без обращения к репозиторию операций.
	txRepo.calls = 0
	if _, err := svc.ListAccountTransactions(ctx, transaction.ListQuery{
		UserID:    user1,
		AccountID: accForeign,
	}); !errors.Is(err, transaction.ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound for foreign account, got %v", err)
	}
	if txRepo.calls != 0 {
		t.Fatalf("expected txRepo not to be called for foreign account, calls=%d", txRepo.calls)
	}
	// 5. Защита на уровне usecase, если AccountReader не отфильтровал userID (bypass = true).
	accRepo.bypass = true
	if _, err := svc.ListAccountTransactions(ctx, transaction.ListQuery{
		UserID:    user1,
		AccountID: accForeign,
	}); !errors.Is(err, transaction.ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound when AccountReader bypasses ownership filter, got %v", err)
	}
	accRepo.bypass = false
}

func TestListAccountTransactions_ValidationErrors(t *testing.T) {
	ctx := context.Background()
	accRepo := &stubAccountReader{}
	txRepo := &stubTransactionRepo{}
	svc := transaction.NewService(accRepo, txRepo)

	t1 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

	invalidQueries := []transaction.ListQuery{
		{UserID: "", AccountID: "acc-1"},
		{UserID: "user-1", AccountID: ""},
		{UserID: "user-1", AccountID: "acc-1", From: &t2, To: &t1}, // From > To
		{UserID: "user-1", AccountID: "acc-1", From: &t1, To: &t1}, // From == To (пустой полуинтервал)
		{UserID: "user-1", AccountID: "acc-1", Limit: -1},
		{UserID: "user-1", AccountID: "acc-1", Limit: transaction.MaxLimit + 1},
		{UserID: "user-1", AccountID: "acc-1", After: &transaction.Cursor{OccurredAt: t1, ID: ""}},
	}

	for i, q := range invalidQueries {
		if _, err := svc.ListAccountTransactions(ctx, q); !errors.Is(err, transaction.ErrInvalidInput) {
			t.Fatalf("case %d: expected ErrInvalidInput, got %v", i, err)
		}
	}
	if accRepo.calls != 0 || txRepo.calls != 0 {
		t.Fatalf("expected no repo calls on invalid input, accCalls=%d txCalls=%d", accRepo.calls, txRepo.calls)
	}
}
