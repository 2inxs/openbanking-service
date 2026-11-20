package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/2inxs/openbanking-service/internal/domain"
	"github.com/2inxs/openbanking-service/internal/usecase"
	"github.com/2inxs/openbanking-service/pkg/logger"
)

const redactedValue = "[REDACTED]"

var (
	ErrAuditUserIDMismatch    = errors.New("audit: event user_id does not match context user_id")
	ErrAuditRequestIDMismatch = errors.New("audit: event request_id does not match context request_id")
)

var sensitiveMetadataTokens = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"authorization",
	"api_key",
	"apikey",
	"credential",
	"private_key",
	"card_number",
	"pan",
	"cvv",
	"cvc",
}

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
		event.ID = newEventID()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}

	// Защитная копия с маскированием секретов до сохранения в память (работает и без техлоггера)
	event.Metadata = sanitizeMetadata(event.Metadata)

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

	// Добавляем user_id и request_id только если их нет в ctx (иначе их запишет contextHandler без дублей)
	if event.UserID != "" && ctxFields.UserID == "" {
		attrs = append(attrs, slog.String("user_id", event.UserID))
	}
	if event.RequestID != "" && ctxFields.RequestID == "" {
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

func sanitizeMetadata(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		if isSensitiveKey(k) {
			dst[k] = redactedValue
			continue
		}
		dst[k] = v
	}
	return dst
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, token := range sensitiveMetadataTokens {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
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
