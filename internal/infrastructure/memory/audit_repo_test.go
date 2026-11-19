package memory_test

import (
	"context"
	"testing"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/infrastructure/memory"
)

func TestAuditRepo_Log(t *testing.T) {
	repo := memory.NewAuditRepo()

	ctx := logger.WithFields(context.Background(), func(f *logger.ContextFields) {
		f.RequestID = "req-abc"
		f.UserID = "user-123"
	})

	meta := map[string]string{"format": "csv"}
	err := repo.Log(ctx, domain.AuditEvent{
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

	got := events
	if got.RequestID != "req-abc" {
		t.Errorf("expected RequestID=req-abc, got %q", got.RequestID)
	}
	if got.Metadata["format"] != "csv" {
		t.Errorf("expected immutable metadata format=csv, got %q", got.Metadata["format"])
	}
	if got.ID == "" || got.OccurredAt.IsZero() {
		t.Error("expected generated ID and OccurredAt")
	}
}

func TestAuditRepo_Validation(t *testing.T) {
	repo := memory.NewAuditRepo()

	if err := repo.Log(context.Background(), domain.AuditEvent{}); err == nil {
		t.Error("expected validation error for empty event")
	}
}
