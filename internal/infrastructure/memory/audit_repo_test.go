package memory_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	loggedMeta, ok := logEntry["metadata"].(map[string]any)
	if !ok || loggedMeta["token"] != "[REDACTED]" {
		t.Errorf("expected redacted token in techlog metadata, got %v", logEntry["metadata"])
	}
}

func TestAuditRepo_Validation(t *testing.T) {
	repo := memory.NewAuditRepo()

	if err := repo.Log(context.Background(), domain.AuditEvent{}); err == nil {
		t.Error("expected validation error for empty event")
	}
}
