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

	// Logging оборачивает Recover, чтобы перехватить итоговый статус 500, выставленный Recover
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

	body := rec.Body.String()
	if strings.Contains(body, "unexpected nil pointer") || strings.Contains(body, "goroutine") {
		t.Fatalf("response leaked internal panic details: %s", body)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 log lines, got %d: %s", len(lines), buf.String())
	}

	// Первая запись — из Recover (срабатывает раньше при раскрутке стека)
	var panicEntry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &panicEntry); err != nil {
		t.Fatalf("invalid json log at line 0: %v", err)
	}
	if panicEntry["msg"] != "panic recovered" {
		t.Errorf("expected msg='panic recovered', got %v", panicEntry["msg"])
	}
	if panicEntry["request_id"] != "panic-req-777" {
		t.Errorf("expected request_id='panic-req-777', got %v", panicEntry["request_id"])
	}
	if panicEntry["stack"] == "" {
		t.Error("expected non-empty stack trace in log")
	}

	// Вторая запись — итоговый лог HTTP-запроса из Logging со статусом 500
	var reqEntry map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &reqEntry); err != nil {
		t.Fatalf("invalid json log at line 1: %v", err)
	}
	if reqEntry["msg"] != "http request" {
		t.Errorf("expected msg='http request', got %v", reqEntry["msg"])
	}
	if reqEntry["status"] != float64(http.StatusInternalServerError) {
		t.Errorf("expected status 500 in http request log, got %v", reqEntry["status"])
	}
}
