package usecase_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/infrastructure/memory"
	"github.com/2inxs/openbanking-service/internal/usecase"
	"github.com/2inxs/openbanking-service/pkg/logger"
)

// RunAuditLoggerContract проверяет поведение любой реализации usecase.AuditLogger
// (in-memory и в будущем PostgreSQL).
func RunAuditLoggerContract(t *testing.T, newRepo func(t *testing.T) (usecase.AuditLogger, func(userID string) []domain.AuditEvent)) {
	t.Helper()

	t.Run("validation errors", func(t *testing.T) {
		repo, _ := newRepo(t)

		if err := repo.Log(context.Background(), domain.AuditEvent{}); !errors.Is(err, domain.ErrAuditActionRequired) {
			t.Fatalf("expected ErrAuditActionRequired, got %v", err)
		}
		if err := repo.Log(context.Background(), domain.AuditEvent{Action: domain.AuditActionLogin}); !errors.Is(err, domain.ErrAuditStatusRequired) {
			t.Fatalf("expected ErrAuditStatusRequired, got %v", err)
		}
		if err := repo.Log(context.Background(), domain.AuditEvent{
			Action: domain.AuditActionLogin,
			Status: "unknown",
		}); !errors.Is(err, domain.ErrAuditStatusInvalid) {
			t.Fatalf("expected ErrAuditStatusInvalid, got %v", err)
		}
	})

	t.Run("sanitizes secrets and amounts and isolates metadata", func(t *testing.T) {
		repo, listByUser := newRepo(t)
		ctx := logger.WithFields(context.Background(), func(f *logger.ContextFields) {
			f.UserID = "u-1"
			f.RequestID = "req-1"
		})

		meta := map[string]string{
			"token":       "secret-token",
			"amount":      "150000",
			"email":       "anna@example.com",
			"card_number": "4276123456789012",
			"format":      "csv",
		}
		if err := repo.Log(ctx, domain.AuditEvent{
			Action:   domain.AuditActionStatementExported,
			Status:   domain.AuditStatusSuccess,
			Metadata: meta,
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		meta["format"] = "mutated"
		events := listByUser("u-1")
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		got := events[0]
		if got.OccurredAt.Location() != time.UTC {
			t.Errorf("expected UTC OccurredAt, got %v", got.OccurredAt.Location())
		}
		if got.Metadata["format"] != "csv" {
			t.Errorf("expected immutable format=csv, got %q", got.Metadata["format"])
		}
		if got.Metadata["token"] != logger.RedactedValue || got.Metadata["amount"] != logger.RedactedValue {
			t.Errorf("expected redacted token and amount, got %+v", got.Metadata)
		}
		if got.Metadata["email"] != "a***@example.com" || got.Metadata["card_number"] != "****9012" {
			t.Errorf("expected masked email/card, got %+v", got.Metadata)
		}
	})

	t.Run("concurrent writes safe under race detector", func(t *testing.T) {
		repo, listByUser := newRepo(t)
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = repo.Log(context.Background(), domain.AuditEvent{
					UserID: "u-race",
					Action: domain.AuditActionLogin,
					Status: domain.AuditStatusSuccess,
				})
			}()
		}
		wg.Wait()

		if got := len(listByUser("u-race")); got != 20 {
			t.Fatalf("expected 20 events, got %d", got)
		}
	})
}

func TestMemoryAuditRepo_Contract(t *testing.T) {
	RunAuditLoggerContract(t, func(t *testing.T) (usecase.AuditLogger, func(userID string) []domain.AuditEvent) {
		r := memory.NewAuditRepo()
		return r, r.ListByUser
	})
}
