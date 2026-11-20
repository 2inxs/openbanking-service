package cbr_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/cbr"
)

const sampleJSON = `{
	"Date": "2026-11-20T11:30:00+03:00",
	"PreviousDate": "2026-11-19T11:30:00+03:00",
	"Timestamp": "2026-11-20T20:00:00+03:00",
	"Valute": {
		"USD": {
			"ID": "R01235",
			"NumCode": "840",
			"CharCode": "USD",
			"Nominal": 1,
			"Name": "Доллар США",
			"Value": 91.4567,
			"Previous": 91.1000
		},
		"KZT": {
			"ID": "R01335",
			"NumCode": "398",
			"CharCode": "KZT",
			"Nominal": 100,
			"Name": "Тенге",
			"Value": 18.5432,
			"Previous": 18.4000
		},
		"BAD1": {
			"ID": "R99999",
			"NumCode": "000",
			"CharCode": "BAD1",
			"Nominal": 1,
			"Name": "Некорректный код",
			"Value": 10.0000,
			"Previous": 10.0000
		}
	}
}`

func TestDefaultEndpoint_PointsToWorkingJSONFeed(t *testing.T) {
	u, err := url.Parse(cbr.DefaultEndpoint)
	if err != nil {
		t.Fatalf("invalid DefaultEndpoint %q: %v", cbr.DefaultEndpoint, err)
	}
	if u.Scheme != "https" || u.Host != "www.cbr-xml-daily.ru" || u.Path != "/daily_json.js" {
		t.Fatalf("unexpected DefaultEndpoint %q, want https://cbr-xml-daily.ru", cbr.DefaultEndpoint)
	}
}

func TestFetchLatest_ParsesDateNominal100RUBAndUnknownCurrency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("unexpected Accept header: %q", got)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(sampleJSON))
	}))
	defer srv.Close()

	client := cbr.NewClient(cbr.WithEndpoint(srv.URL + "/daily_json.js"))
	got, err := client.FetchLatest(context.Background())
	if err != nil {
		t.Fatalf("FetchLatest failed: %v", err)
	}

	wantDate := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	if !got.Date.Equal(wantDate) {
		t.Fatalf("got date %v, want %v", got.Date, wantDate)
	}

	rub, err := got.RateFor(money.RUB)
	if err != nil || rub.RUBPerUnitString() != "1.0000000000" {
		t.Fatalf("RUB rate mismatch: %+v, err=%v", rub, err)
	}

	usd, err := got.RateFor("USD")
	if err != nil || usd.RUBPerUnitString() != "91.4567000000" {
		t.Fatalf("USD rate mismatch: %+v, err=%v", usd, err)
	}

	kzt, err := got.RateFor("KZT")
	if err != nil || kzt.RUBPerUnitString() != "0.1854320000" {
		t.Fatalf("KZT (nominal 100) rate mismatch: %+v, err=%v", kzt, err)
	}

	if _, exists := got.Rates["BAD1"]; exists {
		t.Fatal("invalid currency code BAD1 must be ignored")
	}

	if _, err := got.RateFor("XYZ"); !errors.Is(err, cbr.ErrUnknownCurrency) {
		t.Fatalf("expected ErrUnknownCurrency for XYZ, got %v", err)
	}
}

func TestFetchByDate_RequestsArchivePathAndReadsEffectiveDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/archive/2026/11/22/daily_json.js" {
			t.Fatalf("got path %q, want /archive/2026/11/22/daily_json.js", r.URL.Path)
		}
		_, _ = w.Write([]byte(sampleJSON))
	}))
	defer srv.Close()

	client := cbr.NewClient(cbr.WithEndpoint(srv.URL + "/daily_json.js"))
	reqDate := time.Date(2026, 11, 22, 15, 30, 0, 0, time.UTC)
	got, err := client.FetchByDate(context.Background(), reqDate)
	if err != nil {
		t.Fatalf("FetchByDate failed: %v", err)
	}

	wantEffective := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	if !got.Date.Equal(wantEffective) {
		t.Fatalf("got effective date %v, want %v", got.Date, wantEffective)
	}
}

func TestFetch_RejectsCorruptedAndInvalidRates(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		wantErr error
	}{
		{
			name:    "broken json",
			payload: `{"Date": "2026-11-20T11:30:00+03:00", "Valute": {"USD": {`,
			wantErr: cbr.ErrInvalidResponse,
		},
		{
			name:    "missing date",
			payload: `{"Valute": {"USD": {"CharCode": "USD", "Nominal": 1, "Value": 90.0}}}`,
			wantErr: cbr.ErrInvalidResponse,
		},
		{
			name:    "empty valute map",
			payload: `{"Date": "2026-11-20T11:30:00+03:00", "Valute": {}}`,
			wantErr: cbr.ErrInvalidResponse,
		},
		{
			name:    "zero nominal",
			payload: `{"Date": "2026-11-20T11:30:00+03:00", "Valute": {"USD": {"CharCode": "USD", "Nominal": 0, "Value": 90.0}}}`,
			wantErr: cbr.ErrInvalidRate,
		},
		{
			name:    "negative nominal",
			payload: `{"Date": "2026-11-20T11:30:00+03:00", "Valute": {"USD": {"CharCode": "USD", "Nominal": -10, "Value": 90.0}}}`,
			wantErr: cbr.ErrInvalidRate,
		},
		{
			name:    "zero value",
			payload: `{"Date": "2026-11-20T11:30:00+03:00", "Valute": {"USD": {"CharCode": "USD", "Nominal": 1, "Value": 0}}}`,
			wantErr: cbr.ErrInvalidRate,
		},
		{
			name:    "negative value",
			payload: `{"Date": "2026-11-20T11:30:00+03:00", "Valute": {"USD": {"CharCode": "USD", "Nominal": 1, "Value": -91.5}}}`,
			wantErr: cbr.ErrInvalidRate,
		},
		{
			name:    "non-numeric value",
			payload: `{"Date": "2026-11-20T11:30:00+03:00", "Valute": {"USD": {"CharCode": "USD", "Nominal": 1, "Value": "NaN"}}}`,
			wantErr: cbr.ErrInvalidRate,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.payload))
			}))
			defer srv.Close()

			client := cbr.NewClient(cbr.WithEndpoint(srv.URL))
			_, err := client.FetchLatest(context.Background())
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got err %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestFetch_HandlesTimeoutAndHTTPStatus(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(150 * time.Millisecond)
			_, _ = w.Write([]byte(sampleJSON))
		}))
		defer srv.Close()

		client := cbr.NewClient(cbr.WithEndpoint(srv.URL))
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()

		if _, err := client.FetchLatest(ctx); err == nil {
			t.Fatal("expected timeout error, got nil")
		}
	})

	t.Run("http 503", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		client := cbr.NewClient(cbr.WithEndpoint(srv.URL))
		if _, err := client.FetchLatest(context.Background()); !errors.Is(err, cbr.ErrUnexpectedStatus) {
			t.Fatalf("got err %v, want ErrUnexpectedStatus", err)
		}
	})
}
