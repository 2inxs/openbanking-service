package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/pkg/logger"
)

var (
	ErrAuditUserIDMismatch    = errors.New("audit: event user_id does not match context user_id")
	ErrAuditRequestIDMismatch = errors.New("audit: event request_id does not match context request_id")
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
	if err := event.Validate(); err != nil {
		return err
	}

	ctxFields := logger.FieldsFromContext(ctx)

	userID, err := resolveIdentifier(event.UserID, ctxFields.UserID, ErrAuditUserIDMismatch)
	if err != nil {
		return err
	}
	requestID, err := resolveIdentifier(event.RequestID, ctxFields.RequestID, ErrAuditRequestIDMismatch)
	if err != nil {
		return err
	}

	event.UserID = userID
	event.RequestID = requestID

	if event.ID == "" {
		id, err := newEventID()
		if err != nil {
			return err
		}
		event.ID = id
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	} else {
		event.OccurredAt = event.OccurredAt.UTC()
	}

	// Защитная копия с едиными правилами маскирования (секреты, суммы, email, PAN).
	event.Metadata = logger.SanitizeStringMap(event.Metadata)

	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()

	r.writeTechLog(ctx, ctxFields, event)

	return nil
}

func resolveIdentifier(eventVal, ctxVal string, mismatchErr error) (string, error) {
	switch {
	case eventVal == "":
		return ctxVal, nil
	case ctxVal == "":
		return eventVal, nil
	case eventVal != ctxVal:
		return "", fmt.Errorf("%w: event=%q ctx=%q", mismatchErr, eventVal, ctxVal)
	default:
		return eventVal, nil
	}
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

	if event.UserID != "" {
		attrs = append(attrs, slog.String("user_id", event.UserID))
	}
	if event.RequestID != "" {
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
		attrs = append(attrs, slog.Any("metadata", event.Metadata))
	}

	if event.Status == domain.AuditStatusFailure || event.Action == domain.AuditActionTokenReuseDetected {
		r.log.WarnContext(ctx, "audit event", attrs...)
		return
	}
	r.log.InfoContext(ctx, "audit event", attrs...)
}

// ListByUser возвращает глубокую копию событий пользователя в UTC.
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

func newEventID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("audit: generate event id: %w", err)
	}
	return hex.EncodeToString(b), nil
}
