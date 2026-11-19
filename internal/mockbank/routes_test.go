package mockbank

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/mockbank/dto"
	"github.com/2inxs/openbanking-service/internal/mockbank/model"
	"github.com/2inxs/openbanking-service/internal/mockbank/service"
	"github.com/2inxs/openbanking-service/internal/mockbank/store"
)

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	st := store.NewStore()
	if err := store.SeedDemoData(st); err != nil {
		t.Fatalf("SeedDemoData: %v", err)
	}
	return NewHandler(service.New(st))
}

func serve(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode JSON: %v\nbody: %s", err, rec.Body.String())
	}
}

func TestRoutesStatusCodes(t *testing.T) {
	h := newTestHandler(t)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"health", http.MethodGet, "/health", http.StatusOK},
		{"health wrong method", http.MethodPost, "/health", http.StatusMethodNotAllowed},
		{"unknown route", http.MethodGet, "/unknown", http.StatusNotFound},

		{"accounts", http.MethodGet, basePath + "/accounts", http.StatusOK},
		{"accounts wrong method", http.MethodPost, basePath + "/accounts", http.StatusMethodNotAllowed},

		{"own account", http.MethodGet, basePath + "/accounts/acc-001", http.StatusOK},
		{"missing account", http.MethodGet, basePath + "/accounts/acc-999", http.StatusNotFound},
		{"foreign account", http.MethodGet, basePath + "/accounts/acc-004", http.StatusNotFound},
		{"account wrong method", http.MethodDelete, basePath + "/accounts/acc-001", http.StatusMethodNotAllowed},

		{"own balances", http.MethodGet, basePath + "/accounts/acc-001/balances", http.StatusOK},
		{"missing balances", http.MethodGet, basePath + "/accounts/acc-999/balances", http.StatusNotFound},
		{"foreign balances", http.MethodGet, basePath + "/accounts/acc-004/balances", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(t, h, tt.method, tt.path)
			if rec.Code != tt.wantStatus {
				t.Errorf("got %d, want %d\nbody: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestHealthBody(t *testing.T) {
	rec := serve(t, newTestHandler(t), http.MethodGet, "/health")
	if got := rec.Body.String(); got != "ok" {
		t.Errorf("got body %q, want %q", got, "ok")
	}
}

func TestListAccounts(t *testing.T) {
	h := newTestHandler(t)
	path := basePath + "/accounts"

	var resp dto.AccountsResponse
	decodeJSON(t, serve(t, h, http.MethodGet, path), &resp)

	if resp.Links.Self != path {
		t.Errorf("Links.self = %q, want %q", resp.Links.Self, path)
	}

	got := make(map[string]dto.AccountDTO)
	for _, acc := range resp.Data.Account {
		got[acc.AccountID] = acc
	}

	// Только счета Анны, и все три.
	for _, id := range []string{"acc-001", "acc-002", "acc-003"} {
		if _, ok := got[id]; !ok {
			t.Errorf("account %s is missing", id)
		}
	}
	for _, id := range []string{"acc-004", "acc-005"} {
		if _, ok := got[id]; ok {
			t.Errorf("foreign account %s must not be returned", id)
		}
	}
	if len(got) != 3 {
		t.Errorf("got %d accounts, want 3", len(got))
	}

	acc := got["acc-001"]
	if acc.AccountType != "Personal" || acc.AccountSubType != "CurrentAccount" ||
		acc.Status != "Enabled" || acc.Currency != "RUB" {
		t.Errorf("acc-001 fields: %+v", acc)
	}
	if len(acc.AccountDetails) != 1 ||
		acc.AccountDetails[0].SchemeName != "RU.CBR.AccountNumber" ||
		acc.AccountDetails[0].Identification != "40817810000000000001" {
		t.Errorf("acc-001 AccountDetails: %+v", acc.AccountDetails)
	}

	if usd := got["acc-003"]; usd.Currency != "USD" || usd.AccountSubType != "Savings" {
		t.Errorf("acc-003: currency %q, subtype %q", usd.Currency, usd.AccountSubType)
	}
}

func TestListAccountsJSONFieldNames(t *testing.T) {
	body := serve(t, newTestHandler(t), http.MethodGet, basePath+"/accounts").Body.String()

	for _, field := range []string{
		`"Data"`, `"Account"`, `"accountId"`, `"accountType"`, `"accountSubType"`,
		`"currency"`, `"AccountDetails"`, `"schemeName"`, `"identification"`, `"Links"`, `"self"`, `"Meta"`,
	} {
		if !strings.Contains(body, field) {
			t.Errorf("field %s is missing in JSON: %s", field, body)
		}
	}
}

func TestListAccountsEmptyIsArray(t *testing.T) {
	st := store.NewStore()
	if err := st.AddClient(model.Client{ID: demoClientID, FullName: "Без счетов"}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(service.New(st))

	body := serve(t, h, http.MethodGet, basePath+"/accounts").Body.String()
	if !strings.Contains(body, `"Account":[]`) {
		t.Errorf(`want "Account":[], got %s`, body)
	}
}

func TestGetAccount(t *testing.T) {
	h := newTestHandler(t)
	path := basePath + "/accounts/acc-002"

	var resp dto.AccountsResponse
	decodeJSON(t, serve(t, h, http.MethodGet, path), &resp)

	if len(resp.Data.Account) != 1 {
		t.Fatalf("got %d accounts, want exactly 1 (one account is still an array)", len(resp.Data.Account))
	}
	if acc := resp.Data.Account[0]; acc.AccountID != "acc-002" || acc.AccountSubType != "CreditCard" {
		t.Errorf("got %+v", acc)
	}
	if resp.Links.Self != path {
		t.Errorf("Links.self = %q, want %q", resp.Links.Self, path)
	}
}

func TestNotFoundErrorFormat(t *testing.T) {
	h := newTestHandler(t)

	var resp dto.ErrorResponse
	decodeJSON(t, serve(t, h, http.MethodGet, basePath+"/accounts/acc-999"), &resp)

	if resp.Code != "404" || resp.Message == "" {
		t.Errorf("code %q, message %q", resp.Code, resp.Message)
	}
	if len(resp.Errors) != 1 {
		t.Fatalf("got %d errors, want 1", len(resp.Errors))
	}
	e := resp.Errors[0]
	if e.ErrorCode != string(dto.ErrCodeNotFound) || e.Path != "accountId" || !strings.Contains(e.Message, "acc-999") {
		t.Errorf("error item: %+v", e)
	}
}

func TestForeignAccountLooksLikeMissing(t *testing.T) {
	h := newTestHandler(t)
	for _, suffix := range []string{"", "/balances"} {
		t.Run("accounts/{id}"+suffix, func(t *testing.T) {
			missing := serve(t, h, http.MethodGet, basePath+"/accounts/acc-999"+suffix)
			foreign := serve(t, h, http.MethodGet, basePath+"/accounts/acc-004"+suffix)

			if missing.Code != foreign.Code {
				t.Fatalf("status: missing %d, foreign %d", missing.Code, foreign.Code)
			}
			normalized := strings.ReplaceAll(foreign.Body.String(), "acc-004", "acc-999")
			if normalized != missing.Body.String() {
				t.Errorf("bodies differ:\nmissing: %s\nforeign: %s", missing.Body.String(), foreign.Body.String())
			}
		})
	}
}

func TestBalances(t *testing.T) {
	h := newTestHandler(t)

	tests := []struct {
		accountID     string
		wantAmount    string
		wantCurrency  string
		wantIndicator string
	}{
		{"acc-001", "124341.30", "RUB", "Credit"},
		{"acc-002", "17190.00", "RUB", "Debit"}, // кредитка в минусе: модуль суммы + Debit
		{"acc-003", "1302.50", "USD", "Credit"},
	}
	for _, tt := range tests {
		t.Run(tt.accountID, func(t *testing.T) {
			path := basePath + "/accounts/" + tt.accountID + "/balances"

			var resp dto.BalancesResponse
			decodeJSON(t, serve(t, h, http.MethodGet, path), &resp)

			if len(resp.Data.Balance) != 1 {
				t.Fatalf("got %d balances, want 1", len(resp.Data.Balance))
			}
			b := resp.Data.Balance[0]
			if b.AccountID != tt.accountID || b.Type != "ClosingAvailable" {
				t.Errorf("accountId %q, type %q", b.AccountID, b.Type)
			}
			if b.Amount.Amount != tt.wantAmount || b.Amount.Currency != tt.wantCurrency {
				t.Errorf("amount %s %s, want %s %s", b.Amount.Amount, b.Amount.Currency, tt.wantAmount, tt.wantCurrency)
			}
			if b.CreditDebitIndicator != tt.wantIndicator {
				t.Errorf("creditDebitIndicator %q, want %q", b.CreditDebitIndicator, tt.wantIndicator)
			}
			if _, err := time.Parse(time.RFC3339, b.DateTime); err != nil {
				t.Errorf("dateTime %q is not ISO 8601: %v", b.DateTime, err)
			}
			if resp.Links.Self != path {
				t.Errorf("Links.self = %q, want %q", resp.Links.Self, path)
			}
		})
	}
}
