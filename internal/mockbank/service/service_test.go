package service

import (
	"errors"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/mockbank/model"
	"github.com/2inxs/openbanking-service/internal/mockbank/store"
)

var msk = time.FixedZone("MSK", 3*60*60)

func newSeededService(t *testing.T) *Service {
	t.Helper()
	st := store.NewStore()
	if err := store.SeedDemoData(st); err != nil {
		t.Fatalf("SeedDemoData: %v", err)
	}
	return New(st)
}

type failingStore struct{}

var errBroken = errors.New("storage is broken")

func (failingStore) GetAccount(string) (model.Account, error)   { return model.Account{}, errBroken }
func (failingStore) GetAccountsByClient(string) []model.Account { return nil }
func (failingStore) GetTransactionsByAccount(string) ([]model.Transaction, error) {
	return nil, errBroken
}

func TestAccount(t *testing.T) {
	s := newSeededService(t)

	tests := []struct {
		name      string
		accountID string
		wantErr   error
	}{
		{"own account", "acc-001", nil},
		{"missing account", "acc-999", ErrNotFound},
		{"foreign account", "acc-004", ErrNotFound}, // счёт Бориса
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.Account("cl-001", tt.accountID)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("got error %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestAccountStoreError(t *testing.T) {
	_, err := New(failingStore{}).Account("cl-001", "acc-001")
	if !errors.Is(err, errBroken) || errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want wrapped errBroken (not ErrNotFound)", err)
	}
}

func TestTransactions(t *testing.T) {
	s := newSeededService(t)
	salary := time.Date(2026, 11, 5, 10, 0, 0, 0, msk) // tx-0001

	tests := []struct {
		name      string
		from, to  time.Time
		wantCount int
	}{
		{"no filter", time.Time{}, time.Time{}, 6},
		{"one day", time.Date(2026, 11, 7, 0, 0, 0, 0, msk), time.Date(2026, 11, 7, 23, 59, 59, 0, msk), 2},
		{"bounds are inclusive", salary, salary, 1},
		{"nothing in range", time.Date(2027, 1, 1, 0, 0, 0, 0, msk), time.Time{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txs, err := s.Transactions("cl-001", "acc-001", tt.from, tt.to)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(txs) != tt.wantCount {
				t.Errorf("got %d transactions, want %d", len(txs), tt.wantCount)
			}
		})
	}
}

func TestTransactionsErrors(t *testing.T) {
	s := newSeededService(t)
	from := time.Date(2026, 11, 10, 0, 0, 0, 0, msk)
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, msk)

	if _, err := s.Transactions("cl-001", "acc-001", from, to); !errors.Is(err, ErrInvalidPeriod) {
		t.Errorf("from after to: got %v, want ErrInvalidPeriod", err)
	}
	if _, err := s.Transactions("cl-001", "acc-004", time.Time{}, time.Time{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("foreign account: got %v, want ErrNotFound", err)
	}
}
