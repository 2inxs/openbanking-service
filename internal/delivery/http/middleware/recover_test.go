package middleware_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2inxs/openbanking-service/internal/delivery/http/middleware"
	"github.com/2inxs/openbanking-service/internal/infrastructure/logger"
)

func TestRecoverMiddleware(t *testing.T) {
	var buf bytes.Buffer
	log, err := logger.New("info", &buf)
	if err != nil {
		t.Fatalf("failed to init logger: %v", err)
	}

	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("unexpected nil pointer")
	})

	chain := middleware.RequestID(
		middleware.Logging(log)(
			middleware.Recover(log)(panicHandler),
		),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/boom", nil)
	req.Header.Set("X-Request-Id", "panic-req-777")
	rec := httptest.NewRecorder()

	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}
	if rec.Header().Get("X-Request-Id") != "panic-req-777" {
		t.Errorf("expected X-Request-Id header, got %q", rec.Header().Get("X-Request-Id"))
	}

	// В ответе клиенту не должно быть текста паники или стека
	body := rec.Body.String()
	if strings.Contains(body, "unexpected nil pointer") || strings.Contains(body, "goroutine") {
		t.Fatalf("response leaked internal panic details: %s", body)
	}

	// В логах должна быть запись о панике со стеком и request_id
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) == 0 {
		t.Fatal("expected log output, got empty")
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("invalid json log: %v", err)
	}

	if entry["msg"] != "panic recovered" {
		t.Errorf("expected msg='panic recovered', got %v", entry["msg"])
	}
	if entry["request_id"] != "panic-req-777" {
		t.Errorf("expected request_id='panic-req-777', got %v", entry["request_id"])
	}
	if entry["stack"] == "" {
		t.Error("expected non-empty stack trace in log")
	}
}
