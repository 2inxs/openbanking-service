package usecase

import (
	"context"
	"errors"

	"github.com/2inxs/openbanking-service/internal/domain"
)

var (
	ErrAuditActionRequired = errors.New("audit: action is required")
	ErrAuditStatusRequired = errors.New("audit: status is required")
)

// AuditLogger описывает контракт хранилища аудит-лога (append-only).
// Реализации (in-memory, PostgreSQL) находятся в internal/infrastructure.
type AuditLogger interface {
	Log(ctx context.Context, event domain.AuditEvent) error
}

// ValidateAuditEvent проверяет обязательные инварианты события перед записью.
func ValidateAuditEvent(e domain.AuditEvent) error {
	if e.Action == "" {
		return ErrAuditActionRequired
	}
	if e.Status != domain.AuditStatusSuccess && e.Status != domain.AuditStatusFailure {
		return ErrAuditStatusRequired
	}
	return nil
}
