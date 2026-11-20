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

// encodeWin1251 кодирует кириллицу и ASCII в байты Windows-1251 для имитации реального ответа ЦБ.
func encodeWin1251(s string) []byte {
	var out []byte
	for _, r := range s {
		switch {
		case r < 0x80:
			out = append(out, byte(r))
		case r >= 0x0410 && r <= 0x044F:
			out = append(out, byte(r-0x0410+0xC0))
		case r == 0x0401:
			out = append(out, 0xA8)
		case r == 0x0451:
			out = append(out, 0xB8)
		default:
			out = append(out, '?')
		}
	}
	return out
}

const sampleXML = `<?xml version="1.0" encoding="windows-1251"?>
<ValCurs Date="20.11.2026" name="Foreign Currency Market">
	<Valute ID="R01235">
		<NumCode>840</NumCode>
		<CharCode>USD</CharCode>
		<Nominal>1</Nominal>
		<Name>Доллар США</Name>
		<Value>91,4567</Value>
	</Valute>
	<Valute ID="R01335">
		<NumCode>398</NumCode>
		<CharCode>KZT</CharCode>
		<Nominal>100</Nominal>
		<Name>Тенге</Name>
		<Value>18,5432</Value>
	</Valute>
	<Valute ID="R99999">
		<NumCode>000</NumCode>
		<CharCode>BAD1</CharCode>
		<Nominal>1</Nominal>
		<Name>Некорректный код</Name>
		<Value>10,0000</Value>
	</Valute>
</ValCurs>`

func TestDefaultEndpoint_PointsToOfficialCBRXMLFeed(t *testing.T) {
	u, err := url.Parse(cbr.DefaultEndpoint)
	if err != nil {
		t.Fatalf("invalid DefaultEndpoint %q: %v", cbr.DefaultEndpoint, err)
	}
	if u.Scheme != "https" || u.Host != "www.cbr.ru" || u.Path != "/scripts/XML_daily.asp" {
		t.Fatalf("unexpected DefaultEndpoint %q, want https://www.cbr.ru/scripts/XML_daily.asp", cbr.DefaultEndpoint)
	}
}

func TestFetchLatest_ParsesDateNominal100AndRUB(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("date_req") != "" {
			t.Errorf("unexpected date_req for FetchLatest: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/xml; charset=windows-1251")
		_, _ = w.Write(encodeWin1251(sampleXML))
	}))
	defer srv.Close()

	client := cbr.NewClient(cbr.WithEndpoint(srv.URL))
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

func TestFetchByDate_PassesFormattedQueryAndReadsEffectiveDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("date_req"); got != "22/11/2026" {
			t.Fatalf("got date_req %q, want 22/11/2026", got)
		}
		_, _ = w.Write(encodeWin1251(sampleXML))
	}))
	defer srv.Close()

	client := cbr.NewClient(cbr.WithEndpoint(srv.URL))
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
			name:    "broken xml",
			payload: `<?xml version="1.0" encoding="windows-1251"?><ValCurs Date="20.11.2026"><Valute>`,
			wantErr: cbr.ErrInvalidResponse,
		},
		{
			name:    "missing date",
			payload: `<?xml version="1.0" encoding="utf-8"?><ValCurs><Valute><CharCode>USD</CharCode><Nominal>1</Nominal><Value>90,0</Value></Valute></ValCurs>`,
			wantErr: cbr.ErrInvalidResponse,
		},
		{
			name:    "zero nominal",
			payload: `<?xml version="1.0" encoding="utf-8"?><ValCurs Date="20.11.2026"><Valute><CharCode>USD</CharCode><Nominal>0</Nominal><Value>90,0</Value></Valute></ValCurs>`,
			wantErr: cbr.ErrInvalidRate,
		},
		{
			name:    "negative or zero value",
			payload: `<?xml version="1.0" encoding="utf-8"?><ValCurs Date="20.11.2026"><Valute><CharCode>USD</CharCode><Nominal>1</Nominal><Value>0,0000</Value></Valute></ValCurs>`,
			wantErr: cbr.ErrInvalidRate,
		},
		{
			name:    "non-numeric value",
			payload: `<?xml version="1.0" encoding="utf-8"?><ValCurs Date="20.11.2026"><Valute><CharCode>USD</CharCode><Nominal>1</Nominal><Value>NaN</Value></Valute></ValCurs>`,
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
			_, _ = w.Write([]byte(sampleXML))
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
