package logger

import (
	"context"
	"log/slog"
	"sync"
)

type ctxKey int

const (
	fieldsKey ctxKey = iota + 1
	reqStateKey
)

// ContextFields содержит неизменяемые корреляционные метаданные.
type ContextFields struct {
	RequestID     string
	TraceID       string
	UserID        string
	Job           string
	BankID        string
	InteractionID string
}

// WithFields создаёт копию метаданных родительского контекста, применяет fn
// и возвращает новый context.Context, не затрагивая родительский.
func WithFields(ctx context.Context, fn func(*ContextFields)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	fields := FieldsFromContext(ctx)
	fn(&fields)
	return context.WithValue(ctx, fieldsKey, fields)
}

// FieldsFromContext возвращает метаданные из контекста.
func FieldsFromContext(ctx context.Context) ContextFields {
	if ctx == nil {
		return ContextFields{}
	}
	if f, ok := ctx.Value(fieldsKey).(ContextFields); ok {
		return f
	}
	return ContextFields{}
}

// RequestState хранит данные уровня одного HTTP-запроса, которые устанавливаются
// внутренними middleware (например, Auth) и читаются внешним Logging middleware.
type RequestState struct {
	mu     sync.RWMutex
	userID string
}

// WithRequestState привязывает состояние HTTP-запроса к контексту.
func WithRequestState(ctx context.Context) (context.Context, *RequestState) {
	if ctx == nil {
		ctx = context.Background()
	}
	st := &RequestState{}
	return context.WithValue(ctx, reqStateKey, st), st
}

// SetRequestUserID фиксирует ID аутентифицированного пользователя для access-лога
// и возвращает новый контекст с обновлённым UserID для нижележащих слоёв.
func SetRequestUserID(ctx context.Context, userID string) context.Context {
	if st, ok := ctx.Value(reqStateKey).(*RequestState); ok && st != nil {
		st.mu.Lock()
		st.userID = userID
		st.mu.Unlock()
	}
	return WithFields(ctx, func(f *ContextFields) {
		f.UserID = userID
	})
}

// UserID потокобезопасно возвращает идентификатор пользователя, установленный в рамках запроса.
func (s *RequestState) UserID() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.userID
}

type contextHandler struct {
	next slog.Handler
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	f := FieldsFromContext(ctx)
	if f.RequestID != "" {
		r.AddAttrs(slog.String("request_id", f.RequestID))
	}
	if f.TraceID != "" {
		r.AddAttrs(slog.String("trace_id", f.TraceID))
	}
	if f.UserID != "" {
		r.AddAttrs(slog.String("user_id", f.UserID))
	}
	if f.Job != "" {
		r.AddAttrs(slog.String("job", f.Job))
	}
	if f.BankID != "" {
		r.AddAttrs(slog.String("bank_id", f.BankID))
	}
	if f.InteractionID != "" {
		r.AddAttrs(slog.String("interaction_id", f.InteractionID))
	}
	return h.next.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{next: h.next.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{next: h.next.WithGroup(name)}
}
