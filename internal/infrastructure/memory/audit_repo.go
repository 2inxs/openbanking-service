package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/usecase"
	"github.com/2inxs/openbanking-service/pkg/logger"
)

// AuditRepo реализует usecase.AuditLogger в памяти (append-only)
// и при наличии *slog.Logger проксирует аудит-события в техлоггер.
type AuditRepo struct {
	mu     sync.RWMutex
	events []domain.AuditEvent
	log    *slog.Logger
}

func NewAuditRepo() *AuditRepo {
	return NewAuditRepoWithLogger(nil)
}

func NewAuditRepoWithLogger(log *slog.Logger) *AuditRepo {
	return &AuditRepo{
		events: make([]domain.AuditEvent, 0, 64),
		log:    log,
	}
}

func (r *AuditRepo) Log(ctx context.Context, event domain.AuditEvent) error {
	if err := usecase.ValidateAuditEvent(event); err != nil {
		return err
	}

	// Обогащаем из контекста техлоггера, если поля не переданы явно
	ctxFields := logger.FieldsFromContext(ctx)
	if event.RequestID == "" {
		event.RequestID = ctxFields.RequestID
	}
	if event.UserID == "" {
		event.UserID = ctxFields.UserID
	}

	if event.ID == "" {
		event.ID = newEventID()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}

	// Защитная копия map, чтобы сохранить инвариант неизменяемости записи
	event.Metadata = cloneMetadata(event.Metadata)

	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()

	r.writeTechLog(ctx, ctxFields, event)

	return nil
}

func (r *AuditRepo) writeTechLog(ctx context.Context, ctxFields logger.ContextFields, event domain.AuditEvent) {
	if r.log == nil {
		return
	}

	attrs := []any{
		slog.String("log_type", "audit"),
		slog.String("audit_id", event.ID),
		slog.Time("occurred_at", event.OccurredAt),
		slog.String("action", string(event.Action)),
		slog.String("status", string(event.Status)),
	}

	// Добавляем user_id и request_id только если contextHandler не добавит их сам из ctx
	if event.UserID != "" && event.UserID != ctxFields.UserID {
		attrs = append(attrs, slog.String("user_id", event.UserID))
	}
	if event.RequestID != "" && event.RequestID != ctxFields.RequestID {
		attrs = append(attrs, slog.String("request_id", event.RequestID))
	}

	if event.ResourceID != "" {
		attrs = append(attrs, slog.String("resource_id", event.ResourceID))
	}
	if event.IPAddress != "" {
		attrs = append(attrs, slog.String("ip_address", event.IPAddress))
	}
	if event.UserAgent != "" {
		attrs = append(attrs, slog.String("user_agent", event.UserAgent))
	}
	if len(event.Metadata) > 0 {
		// Передаём через slog.Any, чтобы replaceAttr в pkg/logger замаскировал чувствительные ключи
		attrs = append(attrs, slog.Any("metadata", event.Metadata))
	}

	if event.Status == domain.AuditStatusFailure {
		r.log.WarnContext(ctx, "audit event", attrs...)
		return
	}
	r.log.InfoContext(ctx, "audit event", attrs...)
}

// ListByUser возвращает глубокую копию событий пользователя (удобно для контрактных и интеграционных тестов).
func (r *AuditRepo) ListByUser(userID string) []domain.AuditEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make([]domain.AuditEvent, 0)
	for _, e := range r.events {
		if e.UserID == userID {
			copied := e
			copied.Metadata = cloneMetadata(e.Metadata)
			res = append(res, copied)
		}
	}
	return res
}

func cloneMetadata(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func newEventID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("audit-fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
