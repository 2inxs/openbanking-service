package middleware_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2inxs/openbanking-service/internal/delivery/http/middleware"
	"github.com/2inxs/openbanking-service/pkg/logger"
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

	body := rec.Body.String()
	if strings.Contains(body, "unexpected nil pointer") || strings.Contains(body, "goroutine") {
		t.Fatalf("response leaked internal panic details: %s", body)
	}

	var panicEntry, reqEntry map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("invalid json log %q: %v", line, err)
		}
		switch entry["msg"] {
		case "panic recovered":
			panicEntry = entry
		case "http request":
			reqEntry = entry
		}
	}

	if panicEntry == nil {
		t.Fatalf("expected 'panic recovered' log entry, got: %s", buf.String())
	}
	if panicEntry["request_id"] != "panic-req-777" {
		t.Errorf("expected request_id='panic-req-777', got %v", panicEntry["request_id"])
	}
	if panicEntry["stack"] == "" {
		t.Error("expected non-empty stack trace in log")
	}

	if reqEntry == nil {
		t.Fatalf("expected 'http request' log entry, got: %s", buf.String())
	}
	if reqEntry["status"] != float64(http.StatusInternalServerError) {
		t.Errorf("expected status 500 in http request log, got %v", reqEntry["status"])
	}
}
