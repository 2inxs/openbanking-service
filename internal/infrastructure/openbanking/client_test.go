package openbanking

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testToken = "test-token"

// newBankServer — фальшивый банк: проверяет метод и токен, отдаёт body со статусом status.
func newBankServer(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+testToken {
			t.Errorf("bad request: %s, Authorization %q", r.Method, r.Header.Get("Authorization"))
		}
		w.WriteHeader(status)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c, err := NewClient(baseURL, testToken)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClientSuccess(t *testing.T) {
	ctx := context.Background()

	accounts, err := newTestClient(t, newBankServer(t, 200, readFixture(t, "accounts.json")).URL).Accounts(ctx)
	if err != nil || len(accounts) != 3 {
		t.Fatalf("accounts: got %d, %v", len(accounts), err)
	}

	balance, err := newTestClient(t, newBankServer(t, 200, readFixture(t, "balances_acc-002.json")).URL).Balance(ctx, "acc-002")
	if err != nil || balance.Value.Amount.String() != "-17190.0000" {
		t.Fatalf("balance: got %s, %v", balance.Value.Amount, err)
	}

	txs, err := newTestClient(t, newBankServer(t, 200, readFixture(t, "transactions_acc-001.json")).URL).
		Transactions(ctx, "acc-001", time.Time{}, time.Time{})
	if err != nil || len(txs) != 6 { // Booked и Pending
		t.Fatalf("transactions: got %d, %v", len(txs), err)
	}
}

func TestClientErrorStatuses(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusInternalServerError, ErrUnexpectedStatus},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			// Тело похоже на корректный пустой список: его нельзя принять за успех.
			srv := newBankServer(t, tt.status, []byte(`{"Data":{"Account":[]}}`))
			accounts, err := newTestClient(t, srv.URL).Accounts(context.Background())
			if !errors.Is(err, tt.want) || accounts != nil {
				t.Fatalf("got %v, %v; want %v", accounts, err, tt.want)
			}
			if strings.Contains(err.Error(), testToken) {
				t.Errorf("token leaked into error: %v", err)
			}
		})
	}
}

func TestClientTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // банк «завис» до отмены запроса
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := newTestClient(t, srv.URL).Accounts(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want context.DeadlineExceeded", err)
	}
}

func TestClientInvalidJSON(t *testing.T) {
	srv := newBankServer(t, 200, []byte(`{"Data":`))
	_, err := newTestClient(t, srv.URL).Accounts(context.Background())
	if !errors.Is(err, ErrInvalidBankData) {
		t.Fatalf("got %v, want ErrInvalidBankData", err)
	}
}

func TestClientResponseTooLarge(t *testing.T) {
	srv := newBankServer(t, 200, bytes.Repeat([]byte(" "), maxBodyBytes+1))
	_, err := newTestClient(t, srv.URL).Accounts(context.Background())
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("got %v, want ErrResponseTooLarge", err)
	}
}

// roundTripFunc позволяет перехватить запрос без сети.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Реальный URL запроса: базовый адрес Mock Bank, путь и закодированные даты.
func TestClientRequestURL(t *testing.T) {
	var got string
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.URL.String()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"Data":{"Transaction":[]}}`))}, nil
	})}

	c, err := NewClient("http://127.0.0.1:8081/open-banking/v1/mockbank/", testToken, WithHTTPClient(hc))
	if err != nil {
		t.Fatal(err)
	}
	msk := time.FixedZone("MSK", 3*60*60)
	from := time.Date(2026, 11, 7, 0, 0, 0, 0, msk)
	if _, err := c.Transactions(context.Background(), "acc-001", from, time.Time{}); err != nil {
		t.Fatal(err)
	}

	want := "http://127.0.0.1:8081/open-banking/v1/mockbank/accounts/acc-001/transactions" +
		"?fromBookingDateTime=2026-11-07T00%3A00%3A00%2B03%3A00"
	if got != want {
		t.Errorf("URL:\n got %s\nwant %s", got, want)
	}
}

func TestNewClientInvalidConfig(t *testing.T) {
	tests := []struct{ name, baseURL, token string }{
		{"empty url", "", testToken},
		{"no scheme", "127.0.0.1:8081", testToken},
		{"ftp", "ftp://bank", testToken},
		{"empty token", "http://127.0.0.1:8081", "  "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewClient(tt.baseURL, tt.token); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("got %v, want ErrInvalidConfig", err)
			}
		})
	}
}
