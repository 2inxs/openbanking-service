package mockbank

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2inxs/openbanking-service/internal/mockbank/dto"
	"github.com/2inxs/openbanking-service/internal/mockbank/service"
	"github.com/2inxs/openbanking-service/internal/mockbank/store"
)

const testToken = "test-token"

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	st := store.NewStore()
	if err := store.SeedDemoData(st); err != nil {
		t.Fatalf("SeedDemoData: %v", err)
	}
	auth := AuthConfig{DemoToken: testToken, DemoClientID: "cl-001"}
	return NewHandler(service.New(st), auth)
}

func serve(t *testing.T, h http.Handler, method, target, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	return serve(t, h, http.MethodGet, target, "Bearer "+testToken)
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode JSON: %v\nbody: %s", err, rec.Body.String())
	}
}

func TestStatusCodes(t *testing.T) {
	h := newTestHandler(t)
	auth := "Bearer " + testToken

	tests := []struct {
		name   string
		method string
		path   string
		auth   string
		want   int
	}{
		{"health without token", http.MethodGet, "/health", "", http.StatusOK},
		{"no token", http.MethodGet, basePath + "/accounts", "", http.StatusUnauthorized},
		{"wrong token", http.MethodGet, basePath + "/accounts", "Bearer wrong", http.StatusUnauthorized},
		{"wrong method", http.MethodPost, basePath + "/accounts", auth, http.StatusMethodNotAllowed},

		{"accounts", http.MethodGet, basePath + "/accounts", auth, http.StatusOK},
		{"own account", http.MethodGet, basePath + "/accounts/acc-001", auth, http.StatusOK},
		{"own balances", http.MethodGet, basePath + "/accounts/acc-001/balances", auth, http.StatusOK},
		{"own transactions", http.MethodGet, basePath + "/accounts/acc-001/transactions", auth, http.StatusOK},

		{"missing account", http.MethodGet, basePath + "/accounts/acc-999", auth, http.StatusNotFound},
		{"foreign account", http.MethodGet, basePath + "/accounts/acc-004", auth, http.StatusNotFound},
		{"foreign balances", http.MethodGet, basePath + "/accounts/acc-004/balances", auth, http.StatusNotFound},
		{"foreign transactions", http.MethodGet, basePath + "/accounts/acc-004/transactions", auth, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(t, h, tt.method, tt.path, tt.auth)
			if rec.Code != tt.want {
				t.Errorf("got %d, want %d\nbody: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestUnauthorizedResponse(t *testing.T) {
	rec := serve(t, newTestHandler(t), http.MethodGet, basePath+"/accounts", "")

	if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Errorf("WWW-Authenticate = %q, want Bearer", got)
	}
	var resp dto.ErrorResponse
	decodeJSON(t, rec, &resp)
	if len(resp.Errors) != 1 || resp.Errors[0].ErrorCode != string(dto.ErrCodeInvalidToken) {
		t.Errorf("got %+v, want Token.Invalid", resp)
	}
}

func TestListAccounts(t *testing.T) {
	var resp dto.AccountsResponse
	decodeJSON(t, get(t, newTestHandler(t), basePath+"/accounts"), &resp)

	if len(resp.Data.Account) != 3 {
		t.Fatalf("got %d accounts, want 3 (only cl-001)", len(resp.Data.Account))
	}
	for _, acc := range resp.Data.Account {
		if acc.AccountID == "acc-004" || acc.AccountID == "acc-005" {
			t.Errorf("foreign account %s returned", acc.AccountID)
		}
	}
}

func TestNotFoundResponse(t *testing.T) {
	var resp dto.ErrorResponse
	decodeJSON(t, get(t, newTestHandler(t), basePath+"/accounts/acc-999"), &resp)

	if len(resp.Errors) != 1 {
		t.Fatalf("got %d errors, want 1", len(resp.Errors))
	}
	if e := resp.Errors[0]; e.ErrorCode != string(dto.ErrCodeNotFound) || e.Path != "accountId" {
		t.Errorf("got %+v, want Resource.NotFound at accountId", e)
	}
}

func TestBalances(t *testing.T) {
	h := newTestHandler(t)

	tests := []struct {
		accountID     string
		wantAmount    string
		wantIndicator string
	}{
		{"acc-001", "124341.30", "Credit"},
		{"acc-002", "17190.00", "Debit"}, // кредитка в минусе
	}
	for _, tt := range tests {
		t.Run(tt.accountID, func(t *testing.T) {
			var resp dto.BalancesResponse
			decodeJSON(t, get(t, h, basePath+"/accounts/"+tt.accountID+"/balances"), &resp)

			if len(resp.Data.Balance) != 1 {
				t.Fatalf("got %d balances, want 1", len(resp.Data.Balance))
			}
			b := resp.Data.Balance[0]
			if b.Amount.Amount != tt.wantAmount || b.CreditDebitIndicator != tt.wantIndicator {
				t.Errorf("got %s %s, want %s %s", b.Amount.Amount, b.CreditDebitIndicator, tt.wantAmount, tt.wantIndicator)
			}
		})
	}
}

func TestTransactions(t *testing.T) {
	h := newTestHandler(t)
	path := basePath + "/accounts/acc-001/transactions"

	tests := []struct {
		name      string
		query     string
		wantCount int
	}{
		{"no filter", "", 6},
		{"one day", "?fromBookingDateTime=2026-11-07T00:00:00%2B03:00&toBookingDateTime=2026-11-07T23:59:59%2B03:00", 2},
		{"nothing in range", "?fromBookingDateTime=2027-01-01T00:00:00Z", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp dto.TransactionsResponse
			decodeJSON(t, get(t, h, path+tt.query), &resp)

			if len(resp.Data.Transaction) != tt.wantCount {
				t.Errorf("got %d transactions, want %d", len(resp.Data.Transaction), tt.wantCount)
			}
		})
	}
}

func TestTransactionsEmptyIsArray(t *testing.T) {
	rec := get(t, newTestHandler(t), basePath+"/accounts/acc-001/transactions?fromBookingDateTime=2027-01-01T00:00:00Z")
	if !strings.Contains(rec.Body.String(), `"Transaction":[]`) {
		t.Errorf(`want "Transaction":[], got %s`, rec.Body.String())
	}
}

func TestTransactionsBadDates(t *testing.T) {
	h := newTestHandler(t)

	tests := []struct {
		name     string
		query    string
		wantPath string
	}{
		{"not a date", "fromBookingDateTime=abc", "fromBookingDateTime"},
		{"no time zone", "toBookingDateTime=2026-11-07T00:00:00", "toBookingDateTime"},
		{"from after to", "fromBookingDateTime=2026-11-10T00:00:00Z&toBookingDateTime=2026-11-01T00:00:00Z", "fromBookingDateTime"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := get(t, h, basePath+"/accounts/acc-001/transactions?"+tt.query)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("got %d, want 400", rec.Code)
			}
			var resp dto.ErrorResponse
			decodeJSON(t, rec, &resp)
			if len(resp.Errors) != 1 || resp.Errors[0].Path != tt.wantPath {
				t.Errorf("got %+v, want path %s", resp.Errors, tt.wantPath)
			}
		})
	}
}
