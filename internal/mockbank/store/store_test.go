package store

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/mockbank/model"
)

func newSeededStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	if err := SeedDemoData(s); err != nil {
		t.Fatalf("SeedDemoData: %v", err)
	}
	return s
}

func newStoreWithAccount(t *testing.T, balance int64) *Store {
	t.Helper()
	s := NewStore()
	if err := s.AddClient(model.Client{ID: "cl-1", FullName: "Тест Тестов"}); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	if err := s.AddAccount(model.Account{ID: "acc-1", ClientID: "cl-1", Currency: "RUB", BalanceKop: balance}); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	return s
}

func mustBalance(t *testing.T, s *Store, accountID string) int64 {
	t.Helper()
	acc, err := s.GetAccount(accountID)
	if err != nil {
		t.Fatalf("GetAccount(%q): %v", accountID, err)
	}
	return acc.BalanceKop
}

func TestSeedLoadsData(t *testing.T) {
	s := newSeededStore(t)

	tests := []struct {
		clientID     string
		wantAccounts int
	}{
		{clientID: "cl-001", wantAccounts: 3},
		{clientID: "cl-002", wantAccounts: 2},
		{clientID: "cl-999", wantAccounts: 0},
	}
	for _, tt := range tests {
		t.Run(tt.clientID, func(t *testing.T) {
			got := len(s.GetAccountsByClient(tt.clientID))
			if got != tt.wantAccounts {
				t.Errorf("got %d accounts, want %d", got, tt.wantAccounts)
			}
		})
	}
}

func TestSeedBalances(t *testing.T) {
	s := newSeededStore(t)

	tests := []struct {
		accountID string
		want      int64
	}{
		{accountID: "acc-001", want: 12_434_130}, // 124 341,30 ₽
		{accountID: "acc-002", want: -1_719_000}, // −17 190,00 ₽
		{accountID: "acc-003", want: 130_250},    // 1 302,50 $
		{accountID: "acc-004", want: 7_699_560},  // 76 995,60 ₽
		{accountID: "acc-005", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.accountID, func(t *testing.T) {
			if got := mustBalance(t, s, tt.accountID); got != tt.want {
				t.Errorf("got balance %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGetClient(t *testing.T) {
	s := newSeededStore(t)

	c, err := s.GetClient("cl-001")
	if err != nil {
		t.Fatalf("GetClient(cl-001): %v", err)
	}
	if c.FullName != "Анна Иванова" {
		t.Errorf("got name %q, want %q", c.FullName, "Анна Иванова")
	}

	if _, err := s.GetClient("cl-999"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetClient(cl-999): got error %v, want ErrNotFound", err)
	}
}

func TestGetAccountNotFound(t *testing.T) {
	s := newSeededStore(t)
	_, err := s.GetAccount("acc-999")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got error %v, want ErrNotFound", err)
	}
}

func TestGetTransactionsUnknownAccount(t *testing.T) {
	s := newSeededStore(t)
	_, err := s.GetTransactionsByAccount("acc-999")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got error %v, want ErrNotFound", err)
	}
}

func TestGetTransactionsEmptyAccount(t *testing.T) {
	s := newStoreWithAccount(t, 0)

	txs, err := s.GetTransactionsByAccount("acc-1")
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	if len(txs) != 0 {
		t.Fatalf("got %d transactions, want 0", len(txs))
	}
}

func TestTransactionsNewestFirst(t *testing.T) {
	s := newSeededStore(t)

	txs, err := s.GetTransactionsByAccount("acc-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) < 2 {
		t.Fatalf("need at least 2 transactions to check order, got %d", len(txs))
	}
	for i := 1; i < len(txs); i++ {
		if txs[i-1].BookedAt.Before(txs[i].BookedAt) {
			t.Errorf("wrong order at %d: %s (%s) goes before newer %s (%s)",
				i, txs[i-1].ID, txs[i-1].BookedAt, txs[i].ID, txs[i].BookedAt)
		}
	}
	if txs[0].ID != "tx-0006" {
		t.Errorf("newest transaction: got %s, want tx-0006", txs[0].ID)
	}
}

func TestTransactionsReturnsCopy(t *testing.T) {
	s := newSeededStore(t)

	txs, err := s.GetTransactionsByAccount("acc-001")
	if err != nil {
		t.Fatal(err)
	}
	original := txs[0].AmountKop
	changedID := txs[0].ID

	txs[0].AmountKop = 999_999_999

	again, err := s.GetTransactionsByAccount("acc-001")
	if err != nil {
		t.Fatal(err)
	}
	for _, tx := range again {
		if tx.ID == changedID && tx.AmountKop != original {
			t.Fatalf("store was changed through returned slice: amount %d, want %d", tx.AmountKop, original)
		}
	}
}

func TestAddClientDuplicate(t *testing.T) {
	s := newSeededStore(t)
	err := s.AddClient(model.Client{ID: "cl-001", FullName: "Дубликат"})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("got error %v, want ErrAlreadyExists", err)
	}
	c, _ := s.GetClient("cl-001")
	if c.FullName != "Анна Иванова" {
		t.Errorf("existing client was overwritten: got %q", c.FullName)
	}
}

func TestAddAccountUnknownClient(t *testing.T) {
	s := newSeededStore(t)
	err := s.AddAccount(model.Account{ID: "acc-new", ClientID: "cl-999"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got error %v, want ErrNotFound", err)
	}
	if _, err := s.GetAccount("acc-new"); !errors.Is(err, ErrNotFound) {
		t.Errorf("account was added despite the error")
	}
}

func TestAddAccountDuplicate(t *testing.T) {
	s := newSeededStore(t)
	err := s.AddAccount(model.Account{ID: "acc-001", ClientID: "cl-002"})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("got error %v, want ErrAlreadyExists", err)
	}
	if got := len(s.GetAccountsByClient("cl-002")); got != 2 {
		t.Errorf("cl-002 has %d accounts, want 2", got)
	}
}

func TestAddAccountUpdatesIndex(t *testing.T) {
	s := newStoreWithAccount(t, 0)
	if err := s.AddAccount(model.Account{ID: "acc-2", ClientID: "cl-1"}); err != nil {
		t.Fatal(err)
	}
	accounts := s.GetAccountsByClient("cl-1")
	if len(accounts) != 2 {
		t.Fatalf("got %d accounts, want 2", len(accounts))
	}
	for _, id := range []string{"acc-1", "acc-2"} {
		if _, err := s.GetAccount(id); err != nil {
			t.Errorf("GetAccount(%q): %v", id, err)
		}
	}
}

func TestAddTransactionChangesBalance(t *testing.T) {
	tests := []struct {
		name      string
		direction model.CreditDebit
		amount    int64
		want      int64
	}{
		{name: "credit", direction: model.Credit, amount: 10_000, want: 110_000},
		{name: "debit", direction: model.Debit, amount: 10_000, want: 90_000},
		{name: "debit below zero", direction: model.Debit, amount: 150_000, want: -50_000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStoreWithAccount(t, 100_000) // 1 000 ₽

			err := s.AddTransaction(model.Transaction{
				ID:        "tx-1",
				AccountID: "acc-1",
				AmountKop: tt.amount,
				Direction: tt.direction,
				Status:    model.TransactionBooked,
				BookedAt:  time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC),
			})
			if err != nil {
				t.Fatalf("AddTransaction: %v", err)
			}

			if got := mustBalance(t, s, "acc-1"); got != tt.want {
				t.Errorf("got balance %d, want %d", got, tt.want)
			}
			txs, _ := s.GetTransactionsByAccount("acc-1")
			if len(txs) != 1 || txs[0].ID != "tx-1" {
				t.Errorf("transaction not stored: got %+v", txs)
			}
		})
	}
}

func TestAddTransactionInvalid(t *testing.T) {
	valid := model.Transaction{
		ID:        "tx-1",
		AccountID: "acc-1",
		AmountKop: 10_000,
		Direction: model.Debit,
		Status:    model.TransactionBooked,
		BookedAt:  time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC),
	}

	tests := []struct {
		name    string
		modify  func(tx *model.Transaction)
		wantErr error
	}{
		{name: "unknown account", modify: func(tx *model.Transaction) { tx.AccountID = "acc-999" }, wantErr: ErrNotFound},
		{name: "zero amount", modify: func(tx *model.Transaction) { tx.AmountKop = 0 }},
		{name: "negative amount", modify: func(tx *model.Transaction) { tx.AmountKop = -100 }},
		{name: "unknown direction", modify: func(tx *model.Transaction) { tx.Direction = "Debet" }},
		{name: "empty direction", modify: func(tx *model.Transaction) { tx.Direction = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStoreWithAccount(t, 100_000)
			tx := valid
			tt.modify(&tx)

			err := s.AddTransaction(tx)
			if err == nil {
				t.Fatal("got nil error, want an error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("got error %v, want %v", err, tt.wantErr)
			}
			if got := mustBalance(t, s, "acc-1"); got != 100_000 {
				t.Errorf("balance changed after failed AddTransaction: got %d, want 100000", got)
			}
			txs, _ := s.GetTransactionsByAccount("acc-1")
			if len(txs) != 0 {
				t.Errorf("transaction stored after failed AddTransaction: got %d", len(txs))
			}
		})
	}
}
func TestConcurrentAddTransaction(t *testing.T) {
	s := newStoreWithAccount(t, 0)
	const n = 100

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			err := s.AddTransaction(model.Transaction{
				ID:        fmt.Sprintf("tx-%d", i),
				AccountID: "acc-1",
				AmountKop: 1,
				Direction: model.Credit,
				Status:    model.TransactionBooked,
				BookedAt:  time.Date(2026, 11, 20, 12, 0, i, 0, time.UTC),
			})
			if err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			_, _ = s.GetAccount("acc-1")
			_, _ = s.GetTransactionsByAccount("acc-1")
		}()
	}
	wg.Wait()

	if got := mustBalance(t, s, "acc-1"); got != n {
		t.Errorf("got balance %d, want %d (lost updates?)", got, n)
	}
	txs, _ := s.GetTransactionsByAccount("acc-1")
	if len(txs) != n {
		t.Errorf("got %d transactions, want %d", len(txs), n)
	}
}
