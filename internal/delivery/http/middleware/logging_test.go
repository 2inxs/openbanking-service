package middleware_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/2inxs/openbanking-service/internal/delivery/http/middleware"
	"github.com/2inxs/openbanking-service/internal/infrastructure/logger"
)

func TestLoggingMiddleware(t *testing.T) {
	var buf bytes.Buffer
	log, err := logger.New("info", &buf)
	if err != nil {
		t.Fatalf("failed to init logger: %v", err)
	}

	// Имитируем бизнес-хендлер, перед которым отработал Auth и положил UserID в контекст
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.WithFields(r.Context(), func(f *logger.ContextFields) {
			f.UserID = "user-999"
		})
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	chain := middleware.RequestID(middleware.Logging(log)(handler))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts?secret_token=leak", nil)
	req.Header.Set("X-Request-Id", "custom-req-id")
	rec := httptest.NewRecorder()

	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", rec.Code)
	}

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid json log: %v", err)
	}

	if entry["request_id"] != "custom-req-id" {
		t.Errorf("expected request_id=custom-req-id, got %v", entry["request_id"])
	}
	if entry["user_id"] != "user-999" {
		t.Errorf("expected user_id=user-999, got %v", entry["user_id"])
	}
	if entry["path"] != "/api/v1/accounts" {
		t.Errorf("expected path without query params, got %v", entry["path"])
	}
	if entry["status"] != float64(http.StatusCreated) {
		t.Errorf("expected status 201, got %v", entry["status"])
	}
}
