package httpdelivery_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	httpdelivery "github.com/2inxs/openbanking-service/internal/delivery/http"
	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/usecase/banksync"
)

const (
	demoUser domain.UserID = "user-1"
	mockID   domain.BankID = "bank-1"
)

type banksStub struct{}

func (banksStub) GetByCode(_ context.Context, code domain.BankCode) (domain.BankID, error) {
	if code != "mockbank" {
		return "", banksync.ErrBankNotFound
	}
	return mockID, nil
}

type connectionsStub struct{ items []domain.BankConnection }

func (s *connectionsStub) Create(_ context.Context, c domain.BankConnection) (domain.BankConnection, error) {
	c.BankCode = "mockbank"
	s.items = append(s.items, c)
	return c, nil
}

func (s *connectionsStub) Get(_ context.Context, user domain.UserID, id domain.BankConnectionID) (domain.BankConnection, error) {
	for _, c := range s.items {
		if c.ID == id && c.UserID == user {
			return c, nil
		}
	}
	return domain.BankConnection{}, banksync.ErrConnectionNotFound
}

func (s *connectionsStub) GetByBank(_ context.Context, user domain.UserID, bank domain.BankID) (domain.BankConnection, error) {
	for _, c := range s.items {
		if c.UserID == user && c.BankID == bank {
			return c, nil
		}
	}
	return domain.BankConnection{}, banksync.ErrConnectionNotFound
}

func (s *connectionsStub) UpdateSyncState(context.Context, domain.UserID, domain.BankConnectionID, int64, banksync.ConnectionSyncState) (domain.BankConnection, error) {
	panic("not used")
}

type bankClientStub struct {
	calls int
	err   error
}

func (c *bankClientStub) Accounts(context.Context) ([]banksync.BankAccount, error) {
	c.calls++
	return nil, c.err
}

func (c *bankClientStub) Balance(context.Context, string) (domain.BalanceSnapshot, error) {
	panic("not used")
}

func (c *bankClientStub) Transactions(context.Context, string, time.Time, time.Time) ([]banksync.BankTransaction, error) {
	panic("not used")
}

type idStub struct{}

func (idStub) NewID() (string, error) { return "conn-1", nil }

func newConnectionMux(conns *connectionsStub, client *bankClientStub) *http.ServeMux {
	svc := banksync.NewService(banksStub{}, conns,
		map[domain.BankCode]banksync.BankClient{"mockbank": client}, idStub{})
	h := httpdelivery.NewBankConnectionHandler(svc, func(*http.Request) domain.UserID { return demoUser })
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

func serve(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestConnectBank(t *testing.T) {
	client := &bankClientStub{}
	mux := newConnectionMux(&connectionsStub{}, client)

	first := serve(mux, http.MethodPost, "/api/v1/bank-connections", `{"bank_id":"mockbank"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first POST: status %d, body %s", first.Code, first.Body)
	}
	var got httpdelivery.BankConnectionResponse
	if err := json.Unmarshal(first.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "conn-1" || got.BankID != "mockbank" || got.Status != "active" || got.LastSyncedAt != nil {
		t.Fatalf("first POST: got %+v", got)
	}

	second := serve(mux, http.MethodPost, "/api/v1/bank-connections", `{"bank_id":"mockbank"}`)
	if second.Code != http.StatusOK || client.calls != 1 {
		t.Fatalf("second POST: status %d, bank calls %d, want 200 and 1", second.Code, client.calls)
	}
}

func TestConnectBankErrors(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		bankErr error
		want    int
	}{
		{"client sends user_id", `{"bank_id":"mockbank","user_id":"other"}`, nil, http.StatusBadRequest},
		{"empty bank_id", `{"bank_id":"  "}`, nil, http.StatusBadRequest},
		{"unknown bank", `{"bank_id":"nobank"}`, nil, http.StatusBadRequest},
		{"bank rejected token", `{"bank_id":"mockbank"}`,
			fmt.Errorf("openbanking: token dev_token_123: %w", banksync.ErrBankUnauthorized), http.StatusBadGateway},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := newConnectionMux(&connectionsStub{}, &bankClientStub{err: tt.bankErr})

			rec := serve(mux, http.MethodPost, "/api/v1/bank-connections", tt.body)
			if rec.Code != tt.want {
				t.Fatalf("status %d, want %d, body %s", rec.Code, tt.want, rec.Body)
			}
			if strings.Contains(rec.Body.String(), "dev_token_123") {
				t.Fatalf("token leaked into response: %s", rec.Body)
			}
		})
	}
}

func TestGetConnection(t *testing.T) {
	synced := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	conns := &connectionsStub{items: []domain.BankConnection{
		{ID: "own", UserID: demoUser, BankID: mockID, BankCode: "mockbank", Status: domain.ConnectionError, LastSyncedAt: &synced},
		{ID: "foreign", UserID: "user-2", BankID: mockID, Status: domain.ConnectionActive},
	}}
	client := &bankClientStub{}
	mux := newConnectionMux(conns, client)

	rec := serve(mux, http.MethodGet, "/api/v1/bank-connections/own", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("own: status %d, body %s", rec.Code, rec.Body)
	}
	var got httpdelivery.BankConnectionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.BankID != "mockbank" || got.Status != "error" || got.LastSyncedAt == nil || !got.LastSyncedAt.Equal(synced) {
		t.Fatalf("own: got %+v", got)
	}

	for _, id := range []string{"foreign", "missing"} {
		if rec := serve(mux, http.MethodGet, "/api/v1/bank-connections/"+id, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", id, rec.Code)
		}
	}

	if client.calls != 0 {
		t.Fatalf("GET must not call the bank, calls = %d", client.calls)
	}
}
