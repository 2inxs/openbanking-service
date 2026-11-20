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
	// Добавляем только непустые корреляционные поля, если они ещё не присутствуют в записи.
	existing := make(map[string]struct{}, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		existing[a.Key] = struct{}{}
		return true
	})

	addIfMissing := func(key, val string) {
		if val == "" {
			return
		}
		if _, exists := existing[key]; !exists {
			r.AddAttrs(slog.String(key, val))
		}
	}

	addIfMissing("request_id", f.RequestID)
	addIfMissing("trace_id", f.TraceID)
	addIfMissing("user_id", f.UserID)
	addIfMissing("job", f.Job)
	addIfMissing("bank_id", f.BankID)
	addIfMissing("interaction_id", f.InteractionID)

	return h.next.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{next: h.next.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{next: h.next.WithGroup(name)}
}
