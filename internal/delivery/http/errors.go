package httpdelivery

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/2inxs/openbanking-service/internal/usecase/banksync"
	"github.com/2inxs/openbanking-service/internal/usecase/currency"
)

type apiError struct {
	status  int
	code    string
	message string
}

func resolveError(err error) apiError {
	switch {
	case errors.Is(err, currency.ErrInvalidCurrency):
		return apiError{http.StatusBadRequest, "currency.invalid_code", "Некорректный код валюты"}
	case errors.Is(err, currency.ErrRateNotFound):
		return apiError{http.StatusNotFound, "currency.rate_not_found", "Курс для указанной валюты не найден"}
	case errors.Is(err, banksync.ErrBankNotFound):
		return apiError{http.StatusBadRequest, "bank.not_found", "Банк не найден"}
	case errors.Is(err, banksync.ErrConnectionNotFound):
		return apiError{http.StatusNotFound, "connection.not_found", "Подключение не найдено"}
	case errors.Is(err, banksync.ErrBankUnauthorized),
		errors.Is(err, banksync.ErrBankUnavailable),
		errors.Is(err, banksync.ErrBankInvalidData):
		return apiError{http.StatusBadGateway, "bank.unavailable", "Банк временно недоступен"}

	default:
		return apiError{http.StatusInternalServerError, "internal.error", "Внутренняя ошибка сервера"}
	}
}

func writeUsecaseError(w http.ResponseWriter, r *http.Request, err error) {
	e := resolveError(err)
	if e.status >= http.StatusInternalServerError {
		slog.ErrorContext(r.Context(), "request failed",
			"method", r.Method, "path", r.URL.Path, "err", err)
	}
	writeError(w, e.status, e.code, e.message)
}
