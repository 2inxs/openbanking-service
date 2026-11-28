package httpdelivery

import (
	"net/http"
	"strings"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain/money"
	"github.com/2inxs/openbanking-service/internal/usecase/currency"
)

type RateResponse struct {
	Currency   string `json:"currency_code"`
	RateDate   string `json:"rate_date"`
	RUBPerUnit string `json:"rub_per_unit"`
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
			writeError(w, http.StatusBadRequest,
				"validation.invalid_date", "Некорректный формат даты, ожидается YYYY-MM-DD")
			return
		}
		targetDate = &parsed
	}

	rate, err := h.uc.GetRate(r.Context(), money.Currency(code), targetDate)
	if err != nil {
		writeUsecaseError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, RateResponse{
		Currency:   string(rate.Currency),
		RateDate:   rate.RateDate.Format("2006-01-02"),
		RUBPerUnit: rate.RUBPerUnitString(),
	})
}
