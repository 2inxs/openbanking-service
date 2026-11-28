package banksync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
)

const (
	testUser  domain.UserID   = "user-1"
	otherUser domain.UserID   = "user-2"
	mockbank  domain.BankCode = "mockbank"
	mockID    domain.BankID   = "bank-1"
)

type fakeBanks struct{}

func (fakeBanks) GetByCode(_ context.Context, code domain.BankCode) (domain.BankID, error) {
	if code != mockbank {
		return "", ErrBankNotFound
	}
	return mockID, nil
}

// fakeConnections хранит подключения в памяти.
// Если conflict задан, Create «проигрывает гонку»: сохраняет его и возвращает ErrConnectionConflict.
type fakeConnections struct {
	items    []domain.BankConnection
	conflict *domain.BankConnection
}

func (f *fakeConnections) Create(_ context.Context, c domain.BankConnection) (domain.BankConnection, error) {
	if f.conflict != nil {
		f.items = append(f.items, *f.conflict)
		return domain.BankConnection{}, ErrConnectionConflict
	}
	c.BankCode = mockbank // как подзапрос в connectionColumns
	f.items = append(f.items, c)
	return c, nil
}

func (f *fakeConnections) Get(_ context.Context, user domain.UserID, id domain.BankConnectionID) (domain.BankConnection, error) {
	if id == "not-uuid" {
		return domain.BankConnection{}, domain.ErrInvalidModel
	}
	for _, c := range f.items {
		if c.ID == id && c.UserID == user {
			return c, nil
		}
	}
	return domain.BankConnection{}, ErrConnectionNotFound
}

func (f *fakeConnections) GetByBank(_ context.Context, user domain.UserID, bank domain.BankID) (domain.BankConnection, error) {
	for _, c := range f.items {
		if c.UserID == user && c.BankID == bank {
			return c, nil
		}
	}
	return domain.BankConnection{}, ErrConnectionNotFound
}

func (f *fakeConnections) UpdateSyncState(context.Context, domain.UserID, domain.BankConnectionID, int64, ConnectionSyncState) (domain.BankConnection, error) {
	panic("not used")
}

type fakeClient struct {
	calls int
	err   error
}

func (f *fakeClient) Accounts(context.Context) ([]BankAccount, error) {
	f.calls++
	return nil, f.err
}

func (f *fakeClient) Balance(context.Context, string) (domain.BalanceSnapshot, error) {
	panic("not used")
}

func (f *fakeClient) Transactions(context.Context, string, time.Time, time.Time) ([]BankTransaction, error) {
	panic("not used")
}

type fakeIDs struct{}

func (fakeIDs) NewID() (string, error) { return "conn-1", nil }

func newTestService(conns *fakeConnections, client *fakeClient) *Service {
	return NewService(fakeBanks{}, conns, map[domain.BankCode]BankClient{mockbank: client}, fakeIDs{})
}

func TestConnectCreatesOnceAndReturnsExisting(t *testing.T) {
	conns, client := &fakeConnections{}, &fakeClient{}
	s := newTestService(conns, client)

	first, created, err := s.ConnectBank(context.Background(), testUser, mockbank)
	if err != nil || !created {
		t.Fatalf("first Connect: created=%v err=%v", created, err)
	}
	if first.Status != domain.ConnectionActive || first.BankCode != mockbank {
		t.Fatalf("first Connect: got %+v", first)
	}

	second, created, err := s.ConnectBank(context.Background(), testUser, mockbank)
	if err != nil || created {
		t.Fatalf("second Connect: created=%v err=%v", created, err)
	}
	if second.ID != first.ID {
		t.Fatalf("second Connect: id %q, want %q", second.ID, first.ID)
	}
	if len(conns.items) != 1 || client.calls != 1 {
		t.Fatalf("connections=%d bank calls=%d, want 1 and 1", len(conns.items), client.calls)
	}
}

func TestConnectErrors(t *testing.T) {
	tests := []struct {
		name    string
		code    domain.BankCode
		bankErr error
		want    error
	}{
		{"unknown bank", "nobank", nil, ErrBankNotFound},
		{"bank denied access", mockbank, ErrBankUnauthorized, ErrBankUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conns := &fakeConnections{}
			s := newTestService(conns, &fakeClient{err: tt.bankErr})

			_, _, err := s.ConnectBank(context.Background(), testUser, tt.code)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if len(conns.items) != 0 {
				t.Fatalf("connection must not be created")
			}
		})
	}
}

func TestConnectConflictReturnsWinner(t *testing.T) {
	winner := domain.BankConnection{ID: "winner", UserID: testUser, BankID: mockID, Status: domain.ConnectionActive}
	s := newTestService(&fakeConnections{conflict: &winner}, &fakeClient{})

	got, created, err := s.ConnectBank(context.Background(), testUser, mockbank)
	if err != nil || created || got.ID != "winner" {
		t.Fatalf("got id=%q created=%v err=%v, want winner/false/nil", got.ID, created, err)
	}
}

func TestGet(t *testing.T) {
	synced := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	own := domain.BankConnection{ID: "conn-1", UserID: testUser, BankID: mockID, BankCode: mockbank,
		Status: domain.ConnectionError, LastSyncedAt: &synced}
	client := &fakeClient{}
	s := newTestService(&fakeConnections{items: []domain.BankConnection{own}}, client)

	got, err := s.GetUserConnection(context.Background(), testUser, "conn-1")
	if err != nil {
		t.Fatalf("own: %v", err)
	}
	if got.BankCode != mockbank || got.Status != domain.ConnectionError ||
		got.LastSyncedAt == nil || !got.LastSyncedAt.Equal(synced) {
		t.Fatalf("own: got %+v", got)
	}

	for _, tt := range []struct {
		name string
		user domain.UserID
		id   domain.BankConnectionID
	}{
		{"foreign", otherUser, "conn-1"},
		{"missing", testUser, "conn-404"},
		{"invalid id", testUser, "not-uuid"},
	} {
		if _, err := s.GetUserConnection(context.Background(), tt.user, tt.id); !errors.Is(err, ErrConnectionNotFound) {
			t.Errorf("%s: err = %v, want ErrConnectionNotFound", tt.name, err)
		}
	}

	if client.calls != 0 {
		t.Fatalf("Get must not call the bank, calls = %d", client.calls)
	}
}
