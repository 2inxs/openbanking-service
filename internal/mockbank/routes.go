package mockbank

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/2inxs/openbanking-service/internal/mockbank/dto"
	"github.com/2inxs/openbanking-service/internal/mockbank/service"
	"github.com/2inxs/openbanking-service/internal/mockbank/utils"
)

const (
	basePath     = "/open-banking/v1/mockbank"
	demoClientID = "cl-001"
)

type handler struct {
	svc *service.Service
}

func NewHandler(svc *service.Service) http.Handler {
	h := &handler{svc: svc}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET "+basePath+"/accounts", h.listAccounts)
	mux.HandleFunc("GET "+basePath+"/accounts/{accountId}", h.getAccount)
	mux.HandleFunc("GET "+basePath+"/accounts/{accountId}/balances", h.getBalances)
	mux.HandleFunc("GET "+basePath+"/accounts/{accountId}/transactions", h.getTransactions)

	return mux
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
}

// listAccounts - GET /accounts
func (h *handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	accounts := h.svc.Accounts(demoClientID)

	items := make([]dto.AccountDTO, 0, len(accounts))
	for _, acc := range accounts {
		items = append(items, dto.ToAccountDTO(acc))
	}
	writeJSON(w, http.StatusOK, dto.AccountsResponse{
		Data:  dto.AccountsData{Account: items},
		Links: dto.Links{Self: r.URL.Path},
		Meta:  dto.Meta{},
	})
}

// getAccount - GET /accounts/{accountId}
func (h *handler) getAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("accountId")
	acc, err := h.svc.Account(demoClientID, id)
	if err != nil {
		writeAccountError(w, id, err)
		return
	}
	writeJSON(w, http.StatusOK, dto.AccountsResponse{
		Data:  dto.AccountsData{Account: []dto.AccountDTO{dto.ToAccountDTO(acc)}},
		Links: dto.Links{Self: r.URL.Path},
		Meta:  dto.Meta{},
	})
}

// getBalances - GET /accounts/{accountId}/balances
func (h *handler) getBalances(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("accountId")
	acc, err := h.svc.Account(demoClientID, id)
	if err != nil {
		writeAccountError(w, id, err)
		return
	}
	writeJSON(w, http.StatusOK, dto.BalancesResponse{
		Data:  dto.BalancesData{Balance: []dto.BalanceDTO{dto.ToBalanceDTO(acc, time.Now())}},
		Links: dto.Links{Self: r.URL.Path},
		Meta:  dto.Meta{},
	})
}

// getTransactions - GET /accounts/{accountId}/transactions?fromBookingDateTime=<дата>&toBookingDateTime=<дата>
func (h *handler) getTransactions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("accountId")
	query := r.URL.Query()

	from, err := utils.ParseDateParam(query, "fromBookingDateTime")
	if err != nil {
		writeError(w, http.StatusBadRequest, dto.ErrCodeInvalidField, err.Error(), "fromBookingDateTime")
		return
	}
	to, err := utils.ParseDateParam(query, "toBookingDateTime")
	if err != nil {
		writeError(w, http.StatusBadRequest, dto.ErrCodeInvalidField, err.Error(), "toBookingDateTime")
		return
	}

	acc, err := h.svc.Account(demoClientID, id)
	if err != nil {
		writeAccountError(w, id, err)
		return
	}
	txs, err := h.svc.Transactions(demoClientID, id, from, to)
	if errors.Is(err, service.ErrInvalidPeriod) {
		writeError(w, http.StatusBadRequest, dto.ErrCodeInvalidField, err.Error(), "fromBookingDateTime")
		return
	}
	if err != nil {
		writeAccountError(w, id, err)
		return
	}
	items := make([]dto.TransactionDTO, 0, len(txs))
	for _, tx := range txs {
		items = append(items, dto.ToTransactionDTO(tx, acc))
	}
	writeJSON(w, http.StatusOK, dto.TransactionsResponse{
		Data:  dto.TransactionsData{Transaction: items},
		Links: dto.PageLinks{Self: r.URL.Path},
		Meta:  dto.Meta{},
	})
}

// ErrNotFound 404, всё остальное 500
func writeAccountError(w http.ResponseWriter, accountID string, err error) {
	if errors.Is(err, service.ErrNotFound) {
		writeError(w, http.StatusNotFound, dto.ErrCodeNotFound,
			fmt.Sprintf("account %s not found", accountID), "accountId")
		return
	}
	writeError(w, http.StatusInternalServerError, dto.ErrCodeInternal, "internal error", "")
}

func writeError(w http.ResponseWriter, status int, errorCode dto.ErrorCode, message, path string) {
	resp := dto.ErrorResponse{
		Code:    strconv.Itoa(status),
		Message: message,
		Errors: []dto.ErrorItem{
			{
				ErrorCode: string(errorCode),
				Message:   message,
				Path:      path,
			},
		},
	}
	writeJSON(w, status, resp)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("write response: %v", err)
	}
}
