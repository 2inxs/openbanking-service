package httpdelivery

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/usecase/currency"
)

type RateResponse struct {
	Currency   string `json:"currency"`
	RateDate   string `json:"rate_date"`
	RUBPerUnit string `json:"rub_per_unit"`
}

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type RateHandler struct {
	uc *currency.Service
}

func NewRateHandler(uc *currency.Service) *RateHandler {
	return &RateHandler{uc: uc}
}

func (h *RateHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/rates/{currency}", h.GetRate)
}

func (h *RateHandler) GetRate(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("currency")))
	if code == "" {
		code = strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("currency")))
	}

	var targetDate *time.Time
	if rawDate := strings.TrimSpace(r.URL.Query().Get("date")); rawDate != "" {
		parsed, err := time.Parse("2006-01-02", rawDate)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{
				Code:    "validation.invalid_date",
				Message: "Некорректный формат даты, ожидается YYYY-MM-DD",
			})
			return
		}
		targetDate = &parsed
	}

	rate, err := h.uc.GetRate(r.Context(), money.Currency(code), targetDate)
	if err != nil {
		switch {
		case errors.Is(err, currency.ErrInvalidCurrency):
			writeJSON(w, http.StatusBadRequest, ErrorResponse{
				Code:    "currency.invalid_code",
				Message: "Некорректный код валюты",
			})
		case errors.Is(err, currency.ErrRateNotFound):
			writeJSON(w, http.StatusNotFound, ErrorResponse{
				Code:    "currency.rate_not_found",
				Message: "Курс для указанной валюты не найден",
			})
		default:
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{
				Code:    "internal.error",
				Message: "Внутренняя ошибка сервера",
			})
		}
		return
	}

	writeJSON(w, http.StatusOK, RateResponse{
		Currency:   string(rate.Currency),
		RateDate:   rate.RateDate.Format("2006-01-02"),
		RUBPerUnit: rate.RUBPerUnitString(),
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
