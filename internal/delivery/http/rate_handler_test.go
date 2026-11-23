package httpdelivery_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	httpdelivery "github.com/2inxs/openbanking-service/internal/delivery/http"
	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/memory"
	"github.com/2inxs/openbanking-service/internal/usecase/currency"
)

type stubProvider struct {
	daily domain.DailyRates
	err   error
}

func (s *stubProvider) FetchLatest(_ context.Context) (domain.DailyRates, error) {
	return s.daily, s.err
}

func (s *stubProvider) FetchByDate(_ context.Context, _ time.Time) (domain.DailyRates, error) {
	return s.daily, s.err
}

func TestRateHandler_HTTPAndUsecaseScenarios(t *testing.T) {
	effectiveDate := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	provider := &stubProvider{
		daily: domain.DailyRates{
			Date: effectiveDate,
			Rates: map[money.Currency]domain.ExchangeRate{
				"USD": {
					Currency:   "USD",
					RateDate:   effectiveDate,
					RUBPerUnit: big.NewRat(914567, 10000), // 91.4567
				},
				"KZT": {
					Currency:   "KZT",
					RateDate:   effectiveDate,
					RUBPerUnit: big.NewRat(183168, 1000000), // 0.183168 (номинал 100)
				},
			},
		},
	}

	repo := memory.NewRateRepository()
	uc := currency.NewService(provider, repo)
	handler := httpdelivery.NewRateHandler(uc)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// 1. Первичная и повторная (идемпотентная) загрузка за одну дату
	if err := uc.Refresh(context.Background()); err != nil {
		t.Fatalf("first refresh failed: %v", err)
	}
	if err := uc.Refresh(context.Background()); err != nil {
		t.Fatalf("second idempotent refresh failed: %v", err)
	}

	// 2. Сбой провайдера не затирает ранее сохранённые курсы
	provider.err = errors.New("cbr unavailable")
	if err := uc.Refresh(context.Background()); err == nil {
		t.Fatal("expected refresh error when provider fails")
	}

	t.Run("200 OK with exact 10-decimal string and effective date", func(t *testing.T) {
		// Запрашиваем курс на субботу 2026-11-21 — должен вернуться курс пятницы 2026-11-20
		req := httptest.NewRequest(http.MethodGet, "/api/v1/rates/USD?date=2026-11-21", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("got status %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Fatalf("unexpected Content-Type: %q", ct)
		}

		var got httpdelivery.RateResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if got.Currency != "USD" {
			t.Errorf("got currency %q, want USD", got.Currency)
		}
		if got.RateDate != "2026-11-20" {
			t.Errorf("got rate_date %q, want 2026-11-20", got.RateDate)
		}
		if got.RUBPerUnit != "91.4567000000" {
			t.Errorf("got rub_per_unit %q, want 91.4567000000", got.RUBPerUnit)
		}
	})

	t.Run("200 OK for normalized nominal 100 currency", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/rates/kzt", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("got status %d, want 200", rec.Code)
		}

		var got httpdelivery.RateResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if got.RUBPerUnit != "0.1831680000" {
			t.Errorf("got rub_per_unit %q, want 0.1831680000", got.RUBPerUnit)
		}
	})

	t.Run("200 OK for base currency RUB equals 1", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/rates/RUB?date=2026-11-21", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("got status %d, want 200", rec.Code)
		}

		var got httpdelivery.RateResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if got.RUBPerUnit != "1.0000000000" || got.RateDate != "2026-11-21" {
			t.Errorf("unexpected RUB response: %+v", got)
		}
	})

	t.Run("404 JSON when rate is missing", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/rates/EUR", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("got status %d, want 404", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Fatalf("unexpected Content-Type: %q", ct)
		}

		var errResp httpdelivery.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("unmarshal error response: %v", err)
		}
		if errResp.Code != "currency.rate_not_found" || errResp.Message != "Курс для указанной валюты не найден" {
			t.Fatalf("unexpected error JSON: %+v", errResp)
		}
	})

	t.Run("400 JSON on invalid currency or date format", func(t *testing.T) {
		cases := []struct {
			path        string
			wantCode    string
			wantMessage string
		}{
			{
				path:        "/api/v1/rates/INVALID",
				wantCode:    "currency.invalid_code",
				wantMessage: "Некорректный код валюты",
			},
			{
				path:        "/api/v1/rates/USD?date=20-11-2026",
				wantCode:    "validation.invalid_date",
				wantMessage: "Некорректный формат даты, ожидается YYYY-MM-DD",
			},
		}

		for _, tc := range cases {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s: got status %d, want 400", tc.path, rec.Code)
			}
			var errResp httpdelivery.ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
				t.Fatalf("%s: unmarshal error JSON: %v", tc.path, err)
			}
			if errResp.Code != tc.wantCode || errResp.Message != tc.wantMessage {
				t.Errorf("%s: got %+v, want code=%q message=%q", tc.path, errResp, tc.wantCode, tc.wantMessage)
			}
		}
	})

}
