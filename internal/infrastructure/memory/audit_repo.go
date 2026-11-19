package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/usecase"
)

// AuditRepo реализует usecase.AuditLogger в памяти (append-only).
type AuditRepo struct {
	mu     sync.RWMutex
	events []domain.AuditEvent
}

func NewAuditRepo() *AuditRepo {
	return &AuditRepo{
		events: make([]domain.AuditEvent, 0, 64),
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
	if event.Metadata != nil {
		copied := make(map[string]string, len(event.Metadata))
		for k, v := range event.Metadata {
			copied[k] = v
		}
		event.Metadata = copied
	}

	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()

	return nil
}

// ListByUser возвращает копию событий пользователя (удобно для контрактных и интеграционных тестов).
func (r *AuditRepo) ListByUser(userID string) []domain.AuditEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make([]domain.AuditEvent, 0)
	for _, e := range r.events {
		if e.UserID == userID {
			res = append(res, e)
		}
	}
	return res
}

func newEventID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "audit-fallback-id"
	}
	return hex.EncodeToString(b)
}
