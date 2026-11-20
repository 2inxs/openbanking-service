package usecase

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/2inxs/openbanking-service/internal/domain"
)

var (
	ErrAuditActionRequired = domain.ErrAuditActionRequired
	ErrAuditActionInvalid  = domain.ErrAuditActionInvalid
	ErrAuditStatusRequired = domain.ErrAuditStatusRequired
	ErrAuditStatusInvalid  = domain.ErrAuditStatusInvalid
)

// AuditLogger описывает контракт хранилища аудит-лога (append-only).
// Реализации (in-memory, PostgreSQL) находятся в internal/infrastructure.
type AuditLogger interface {
	Log(ctx context.Context, event domain.AuditEvent) error
}

// AuditMetrics отслеживает события аудита и ошибки записи (для алертов A09).
type AuditMetrics interface {
	IncAuditRecorded(action domain.AuditAction, status domain.AuditStatus)
	IncAuditWriteError(action domain.AuditAction)
}

// ValidateAuditEvent проверяет обязательные инварианты события перед записью.
func ValidateAuditEvent(e domain.AuditEvent) error {
	return e.Validate()
}

// AuditRecorder оборачивает AuditLogger: проверяет событие, пишет метрики
// и поддерживает два режима при сбое хранилища: строгий (MustLog) и неблокирующий (BestEffortLog).
type AuditRecorder struct {
	repo    AuditLogger
	metrics AuditMetrics
	log     *slog.Logger
}

func NewAuditRecorder(repo AuditLogger, metrics AuditMetrics, log *slog.Logger) *AuditRecorder {
	return &AuditRecorder{
		repo:    repo,
		metrics: metrics,
		log:     log,
	}
}

// MustLog записывает событие и возвращает ошибку при сбое (fail-closed для критичных операций:
// смена пароля, удаление аккаунта, отзыв/компрометация токена).
func (r *AuditRecorder) MustLog(ctx context.Context, event domain.AuditEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if err := r.repo.Log(ctx, event); err != nil {
		if r.metrics != nil {
			r.metrics.IncAuditWriteError(event.Action)
		}
		return fmt.Errorf("audit log (%s): %w", event.Action, err)
	}
	if r.metrics != nil {
		r.metrics.IncAuditRecorded(event.Action, event.Status)
	}
	return nil
}

// BestEffortLog пытается записать событие аудита, но при ошибке хранилища
// фиксирует сбой в метрике/логе и не прерывает основной бизнес-сценарий.
func (r *AuditRecorder) BestEffortLog(ctx context.Context, event domain.AuditEvent) {
	if err := r.MustLog(ctx, event); err != nil && r.log != nil {
		r.log.ErrorContext(ctx, "audit write failed",
			slog.String("audit_action", string(event.Action)),
			slog.Any("error", err),
		)
	}
}
