package httpdelivery

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/usecase/banksync"
)

var connectionStatusNames = map[domain.ConnectionStatusID]string{
	domain.ConnectionActive:       "active",
	domain.ConnectionError:        "error",
	domain.ConnectionDisconnected: "disconnected",
	domain.ConnectionPending:      "pending",
}

type connectBankRequest struct {
	BankCode string `json:"bank_code"`
}

type BankConnectionResponse struct {
	ID           string     `json:"id"`
	BankCode     string     `json:"bank_code"`
	Status       string     `json:"status"`
	LastSyncedAt *time.Time `json:"last_synced_at"`
}

type BankConnectionHandler struct {
	uc          *banksync.Service
	currentUser func(*http.Request) domain.UserID
}

func NewBankConnectionHandler(uc *banksync.Service, currentUser func(*http.Request) domain.UserID) *BankConnectionHandler {
	return &BankConnectionHandler{uc: uc, currentUser: currentUser}
}

func (h *BankConnectionHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/bank-connections", h.Connect)
	mux.HandleFunc("GET /api/v1/bank-connections/{connectionId}", h.GetConnection)
}

func (h *BankConnectionHandler) Connect(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req connectBankRequest
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "validation.invalid_body", "Некорректное тело запроса")
		return
	}
	bankCode := strings.TrimSpace(req.BankCode)
	if bankCode == "" {
		writeError(w, http.StatusBadRequest, "validation.bank_code_required", "Не указан bank_code")
		return
	}
	conn, created, err := h.uc.ConnectBank(r.Context(), h.currentUser(r), domain.BankCode(bankCode))
	if err != nil {
		writeUsecaseError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, toResponse(conn))
}

func (h *BankConnectionHandler) GetConnection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("connectionId")
	conn, err := h.uc.GetUserConnection(r.Context(), h.currentUser(r), domain.BankConnectionID(id))
	if err != nil {
		writeUsecaseError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toResponse(conn))
}

func toResponse(c domain.BankConnection) BankConnectionResponse {
	return BankConnectionResponse{
		ID:           string(c.ID),
		BankCode:     string(c.BankCode),
		Status:       connectionStatusNames[c.Status],
		LastSyncedAt: c.LastSyncedAt,
	}
}
