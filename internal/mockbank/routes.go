package mockbank

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/2inxs/openbanking-service/internal/mockbank/dto"
	"github.com/2inxs/openbanking-service/internal/mockbank/store"
)

const (
	basePath     = "/open-banking/v1/mockbank"
	demoClientID = "cl-001"
)

type handler struct {
	store *store.Store // здесь хранилище будет лежать, пока работает сервер
}

func NewHandler(store *store.Store) http.Handler {
	h := &handler{store: store}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET "+basePath+"/accounts", h.listAccounts)
	mux.HandleFunc("GET "+basePath+"/accounts/{accountId}", h.getAccount)
	return mux
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
}

func (h *handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	accounts := h.store.GetAccountsByClient(demoClientID)
	accountDto := make([]dto.AccountDTO, 0, len(accounts))
	for _, account := range accounts {
		accountDto = append(accountDto, dto.ToAccountDTO(account))
	}
	resp := dto.AccountsResponse{
		Data:  dto.AccountsData{Account: accountDto},
		Links: dto.Links{Self: r.URL.Path},
		Meta:  dto.Meta{},
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) getAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("accountId")

	account, err := h.store.GetAccount(id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, dto.ErrCodeNotFound,
			fmt.Sprintf("account %s not found", id), "accountId")
		return
	}
	if err != nil {
		log.Printf("get account %s: %v", id, err)
		writeError(w, http.StatusInternalServerError, dto.ErrCodeInternal,
			"internal error", "")
		return
	}

	if account.ClientID != demoClientID {
		writeError(w, http.StatusNotFound, dto.ErrCodeNotFound,
			fmt.Sprintf("account %s not found", id), "accountId")
		return
	}

	resp := dto.AccountsResponse{
		Data:  dto.AccountsData{Account: []dto.AccountDTO{dto.ToAccountDTO(account)}},
		Links: dto.Links{Self: r.URL.Path},
		Meta:  dto.Meta{},
	}
	writeJSON(w, http.StatusOK, resp)
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
