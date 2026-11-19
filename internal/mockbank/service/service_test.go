package service

import (
	"errors"
	"testing"

	"github.com/2inxs/openbanking-service/internal/mockbank/model"
	"github.com/2inxs/openbanking-service/internal/mockbank/store"
)

// newSeededService создаёт сервис поверх настоящего хранилища с демо-данными.
func newSeededService(t *testing.T) *Service {
	t.Helper()
	st := store.NewStore()
	if err := store.SeedDemoData(st); err != nil {
		t.Fatalf("SeedDemoData: %v", err)
	}
	return New(st)
}

func TestAccounts(t *testing.T) {
	s := newSeededService(t)

	if got := len(s.Accounts("cl-001")); got != 3 {
		t.Errorf("cl-001: got %d accounts, want 3", got)
	}
	if got := len(s.Accounts("cl-999")); got != 0 {
		t.Errorf("unknown client: got %d accounts, want 0", got)
	}
}

func TestAccount(t *testing.T) {
	s := newSeededService(t)

	tests := []struct {
		name      string
		clientID  string
		accountID string
		wantErr   error
	}{
		{name: "own account", clientID: "cl-001", accountID: "acc-001", wantErr: nil},
		{name: "missing account", clientID: "cl-001", accountID: "acc-999", wantErr: ErrNotFound},
		{name: "foreign account", clientID: "cl-001", accountID: "acc-004", wantErr: ErrNotFound}, // счёт Бориса
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc, err := s.Account(tt.clientID, tt.accountID)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && acc.ID != tt.accountID {
				t.Errorf("got account %q, want %q", acc.ID, tt.accountID)
			}
		})
	}
}

// failingStore — заглушка хранилища, которое всегда падает с внутренней ошибкой.
// Показывает, зачем сервису интерфейс Store: в тесте его можно подменить.
type failingStore struct{}

var errBroken = errors.New("storage is broken")

func (failingStore) GetAccount(string) (model.Account, error)   { return model.Account{}, errBroken }
func (failingStore) GetAccountsByClient(string) []model.Account { return nil }

func TestAccountInternalError(t *testing.T) {
	s := New(failingStore{})

	_, err := s.Account("cl-001", "acc-001")
	if errors.Is(err, ErrNotFound) {
		t.Fatal("internal error must not look like ErrNotFound")
	}
	if !errors.Is(err, errBroken) {
		t.Fatalf("got %v, want wrapped errBroken", err)
	}
}
