package memory_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/infrastructure/memory"
	"github.com/2inxs/openbanking-service/pkg/logger"
)

func TestAuditRepo_Log(t *testing.T) {
	var buf bytes.Buffer
	techLog, err := logger.New("info", &buf)
	if err != nil {
		t.Fatalf("failed to init logger: %v", err)
	}

	repo := memory.NewAuditRepoWithLogger(techLog)

	ctx := logger.WithFields(context.Background(), func(f *logger.ContextFields) {
		f.RequestID = "req-abc"
		f.UserID = "user-123"
	})

	meta := map[string]string{
		"format": "csv",
		"token":  "super-secret-token",
	}
	err = repo.Log(ctx, domain.AuditEvent{
		Action:     domain.AuditActionStatementExported,
		Status:     domain.AuditStatusSuccess,
		ResourceID: "acc-001",
		Metadata:   meta,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Мутируем исходную map и проверяем, что запись в аудите не изменилась
	meta["format"] = "hacked"

	events := repo.ListByUser("user-123")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	got := events[0]
	if got.RequestID != "req-abc" {
		t.Errorf("expected RequestID=req-abc, got %q", got.RequestID)
	}
	if got.Metadata["format"] != "csv" {
		t.Errorf("expected immutable metadata format=csv, got %q", got.Metadata["format"])
	}
	if got.Metadata["token"] != "[REDACTED]" {
		t.Errorf("expected redacted token in memory storage, got %q", got.Metadata["token"])
	}
	if got.ID == "" || got.OccurredAt.IsZero() {
		t.Error("expected generated ID and OccurredAt")
	}

	// Проверяем, что событие записалось в techlogger и чувствительные поля замаскированы
	var logEntry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("expected valid json log, got %q: %v", buf.String(), err)
	}
	if logEntry["log_type"] != "audit" {
		t.Errorf("expected log_type=audit, got %v", logEntry["log_type"])
	}
	if logEntry["action"] != string(domain.AuditActionStatementExported) {
		t.Errorf("expected action=%s, got %v", domain.AuditActionStatementExported, logEntry["action"])
	}
	if logEntry["user_id"] != "user-123" {
		t.Errorf("expected user_id=user-123, got %v", logEntry["user_id"])
	}
	if logEntry["request_id"] != "req-abc" {
		t.Errorf("expected request_id=req-abc, got %v", logEntry["request_id"])
	}
	loggedMeta, ok := logEntry["metadata"].(map[string]any)
	if !ok || loggedMeta["token"] != "[REDACTED]" {
		t.Errorf("expected redacted token in techlog metadata, got %v", logEntry["metadata"])
	}
}

func TestAuditRepo_MetadataRedactedWithoutLogger(t *testing.T) {
	repo := memory.NewAuditRepo()

	err := repo.Log(context.Background(), domain.AuditEvent{
		UserID: "user-123",
		Action: domain.AuditActionStatementExported,
		Status: domain.AuditStatusSuccess,
		Metadata: map[string]string{
			"access_token": "secret-access",
			"password":     "qwerty123456",
			"pan":          "4111111111111111",
			"cvv":          "123",
			"format":       "csv",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := repo.ListByUser("user-123")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	gotMeta := events[0].Metadata
	for _, secretKey := range []string{"access_token", "password", "pan", "cvv"} {
		if gotMeta[secretKey] != "[REDACTED]" {
			t.Errorf("expected %s to be redacted in storage, got %q", secretKey, gotMeta[secretKey])
		}
	}
	if gotMeta["format"] != "csv" {
		t.Errorf("expected non-sensitive key format=csv, got %q", gotMeta["format"])
	}
}

func TestAuditRepo_IDConflict(t *testing.T) {
	var buf bytes.Buffer
	techLog, err := logger.New("info", &buf)
	if err != nil {
		t.Fatalf("failed to init logger: %v", err)
	}

	repo := memory.NewAuditRepoWithLogger(techLog)

	ctx := logger.WithFields(context.Background(), func(f *logger.ContextFields) {
		f.UserID = "context-user"
		f.RequestID = "context-req"
	})

	t.Run("user_id mismatch", func(t *testing.T) {
		err := repo.Log(ctx, domain.AuditEvent{
			UserID:    "explicit-user",
			RequestID: "context-req",
			Action:    domain.AuditActionStatementExported,
			Status:    domain.AuditStatusSuccess,
		})
		if !errors.Is(err, memory.ErrAuditUserIDMismatch) {
			t.Fatalf("expected ErrAuditUserIDMismatch, got %v", err)
		}
		if len(repo.ListByUser("context-user")) != 0 || len(repo.ListByUser("explicit-user")) != 0 {
			t.Error("expected no events stored on user_id conflict")
		}
		if buf.Len() != 0 {
			t.Errorf("expected no techlog written on conflict, got %q", buf.String())
		}
	})

	t.Run("request_id mismatch", func(t *testing.T) {
		err := repo.Log(ctx, domain.AuditEvent{
			UserID:    "context-user",
			RequestID: "explicit-req",
			Action:    domain.AuditActionStatementExported,
			Status:    domain.AuditStatusSuccess,
		})
		if !errors.Is(err, memory.ErrAuditRequestIDMismatch) {
			t.Fatalf("expected ErrAuditRequestIDMismatch, got %v", err)
		}
		if len(repo.ListByUser("context-user")) != 0 {
			t.Error("expected no events stored on request_id conflict")
		}
		if buf.Len() != 0 {
			t.Errorf("expected no techlog written on conflict, got %q", buf.String())
		}
	})
}

func TestAuditRepo_ExplicitIDsWithoutContext(t *testing.T) {
	var buf bytes.Buffer
	techLog, err := logger.New("info", &buf)
	if err != nil {
		t.Fatalf("failed to init logger: %v", err)
	}

	repo := memory.NewAuditRepoWithLogger(techLog)

	err = repo.Log(context.Background(), domain.AuditEvent{
		UserID:    "explicit-user",
		RequestID: "explicit-req",
		Action:    domain.AuditActionStatementExported,
		Status:    domain.AuditStatusSuccess,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rawJSON := buf.String()
	if strings.Count(rawJSON, `"user_id"`) != 1 {
		t.Errorf("expected exactly one user_id key in json log, got: %s", rawJSON)
	}
	if strings.Count(rawJSON, `"request_id"`) != 1 {
		t.Errorf("expected exactly one request_id key in json log, got: %s", rawJSON)
	}

	var logEntry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to unmarshal log: %v", err)
	}
	if logEntry["user_id"] != "explicit-user" || logEntry["request_id"] != "explicit-req" {
		t.Errorf("unexpected IDs in log entry: %v", logEntry)
	}
}

func TestAuditRepo_Validation(t *testing.T) {
	repo := memory.NewAuditRepo()

	if err := repo.Log(context.Background(), domain.AuditEvent{}); err == nil {
		t.Error("expected validation error for empty event")
	}
}
