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
	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/infrastructure/cbr"
	"github.com/2inxs/openbanking-service/internal/infrastructure/memory"
	"github.com/2inxs/openbanking-service/internal/usecase/currency"
)

type stubProvider struct {
	daily cbr.DailyRates
	err   error
}

func (s *stubProvider) FetchLatest(_ context.Context) (cbr.DailyRates, error) {
	return s.daily, s.err
}

func (s *stubProvider) FetchByDate(_ context.Context, _ time.Time) (cbr.DailyRates, error) {
	return s.daily, s.err
}

func TestRateHandler_EndToEndWithMemoryRepo(t *testing.T) {
	effectiveDate := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	provider := &stubProvider{
		daily: cbr.DailyRates{
			Date: effectiveDate,
			Rates: map[money.Currency]cbr.Rate{
				"USD": {
					Currency:   "USD",
					RateDate:   effectiveDate,
					RUBPerUnit: big.NewRat(914567, 10000),
				},
			},
		},
	}

	repo := memory.NewRateRepository()
	uc := currency.NewService(provider, repo)
	handler := httpdelivery.NewRateHandler(uc)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// 1. Первая и повторная (идемпотентная) загрузка за одну дату
	if _, err := uc.SyncLatest(context.Background()); err != nil {
		t.Fatalf("first sync failed: %v", err)
	}
	if _, err := uc.SyncLatest(context.Background()); err != nil {
		t.Fatalf("second sync failed: %v", err)
	}

	// 2. Сбой провайдера не затирает старые курсы
	provider.err = errors.New("cbr unavailable")
	if _, err := uc.SyncLatest(context.Background()); err == nil {
		t.Fatal("expected sync error")
	}

	// 3. Успешный JSON-ответ на выходной день (фактическая дата 2026-11-20)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/rates/USD?date=2026-11-22", nil)
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
		t.Fatalf("unmarshal ok response: %v", err)
	}
	if got.Currency != "USD" || got.RateDate != "2026-11-20" || got.RUBPerUnit != "91.4567000000" {
		t.Fatalf("unexpected response: %+v", got)
	}

	// 4. Отсутствующая валюта -> 404 JSON в формате проекта
	req404 := httptest.NewRequest(http.MethodGet, "/api/v1/rates/EUR", nil)
	rec404 := httptest.NewRecorder()
	mux.ServeHTTP(rec404, req404)

	if rec404.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404", rec404.Code)
	}
	var errResp httpdelivery.ErrorResponse
	if err := json.Unmarshal(rec404.Body.Bytes(), &errResp); err != nil || errResp.Code != "currency.rate_not_found" {
		t.Fatalf("unexpected error JSON: %s", rec404.Body.String())
	}
}
