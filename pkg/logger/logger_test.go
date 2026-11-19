package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/2inxs/openbanking-service/pkg/logger"
)

func TestLogger_SanitizesSensitiveData(t *testing.T) {
	var buf bytes.Buffer
	log, err := logger.New("debug", &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := logger.WithFields(context.Background(), func(f *logger.ContextFields) {
		f.RequestID = "req-123"
		f.UserID = "user-uuid-456"
		f.BankID = "sber"
	})

	log.InfoContext(ctx, "user action",
		slog.String("password", "SuperSecret123!"),
		slog.String("access_token", "eyJhbGciOiJFZERTQSJ9.payload.sig"),
		slog.String("email", "ivan.ivanov@example.com"),
		slog.String("pan", "4276 1234 5678 9012"),
		slog.Int64("amount", 150050),
		slog.Group("nested",
			slog.String("client_secret", "top-secret"),
			slog.String("account_number", "40817810621234567890"),
		),
	)

	rawOutput := buf.String()

	// Проверяем, что исходные секреты и PII полностью отсутствуют в выводе
	forbidden := []string{
		"SuperSecret123!",
		"eyJhbGciOiJFZERTQSJ9",
		"ivan.ivanov@example.com",
		"4276 1234 5678 9012",
		"5678",
		"150050",
		"top-secret",
		"4081781062123456",
	}
	for _, secret := range forbidden {
		if strings.Contains(rawOutput, secret) {
			t.Errorf("log output leaked sensitive value %q: %s", secret, rawOutput)
		}
	}

	// Проверяем структуру JSON и корректность маскирования
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("invalid json output: %v", err)
	}

	if entry["request_id"] != "req-123" {
		t.Errorf("expected request_id=req-123, got %v", entry["request_id"])
	}
	if entry["user_id"] != "user-uuid-456" {
		t.Errorf("expected user_id=user-uuid-456, got %v", entry["user_id"])
	}
	if entry["password"] != "[REDACTED]" {
		t.Errorf("expected redacted password, got %v", entry["password"])
	}
	if entry["email"] != "i***@example.com" {
		t.Errorf("expected masked email i***@example.com, got %v", entry["email"])
	}
	if entry["pan"] != "****9012" {
		t.Errorf("expected masked pan ****9012, got %v", entry["pan"])
	}

	nested, ok := entry["nested"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested group in json")
	}
	if nested["client_secret"] != "[REDACTED]" {
		t.Errorf("expected redacted nested client_secret, got %v", nested["client_secret"])
	}
	if nested["account_number"] != "****7890" {
		t.Errorf("expected masked nested account_number ****7890, got %v", nested["account_number"])
	}
}

func TestLogger_InvalidLevel(t *testing.T) {
	_, err := logger.New("verbose", nil)
	if err == nil {
		t.Fatal("expected error for invalid log level, got nil")
	}
}

func TestContextFields_Concurrent(t *testing.T) {
	var buf bytes.Buffer
	log, err := logger.New("info", &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := logger.WithFields(context.Background(), func(f *logger.ContextFields) {
		f.RequestID = "req-concurrent"
	})
	ctx, state := logger.WithRequestState(ctx)

	const goroutines = 20
	done := make(chan struct{}, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			if id%2 == 0 {
				childCtx := logger.WithFields(ctx, func(f *logger.ContextFields) {
					f.BankID = "bank-child"
				})
				if logger.FieldsFromContext(childCtx).BankID != "bank-child" {
					t.Errorf("expected child BankID=bank-child")
				}
				logger.SetRequestUserID(ctx, "user-updated")
			} else {
				_ = state.UserID()
				log.InfoContext(ctx, "concurrent log")
			}
		}(i)
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	got := logger.FieldsFromContext(ctx)
	if got.RequestID != "req-concurrent" {
		t.Errorf("expected RequestID=req-concurrent, got %q", got.RequestID)
	}
	if got.UserID != "" || got.BankID != "" {
		t.Errorf("expected parent context to remain immutable, got UserID=%q BankID=%q", got.UserID, got.BankID)
	}
	if state.UserID() != "user-updated" {
		t.Errorf("expected RequestState UserID=user-updated, got %q", state.UserID())
	}
}
