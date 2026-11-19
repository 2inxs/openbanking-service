package logger

import (
	"context"
	"log/slog"
)

type ctxKey int

const (
	fieldsKey ctxKey = iota + 1
)

// ContextFields содержит корреляционные метаданные запроса или фоновой задачи.
type ContextFields struct {
	RequestID     string
	TraceID       string
	UserID        string
	Job           string
	BankID        string
	InteractionID string // x-fapi-interaction-id для запросов к банкам
}

// WithFields добавляет или обновляет поля логирования в контексте.
func WithFields(ctx context.Context, fn func(*ContextFields)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	current := FieldsFromContext(ctx)
	fn(&current)
	return context.WithValue(ctx, fieldsKey, current)
}

// FieldsFromContext извлекает метаданные из контекста.
func FieldsFromContext(ctx context.Context) ContextFields {
	if ctx == nil {
		return ContextFields{}
	}
	if f, ok := ctx.Value(fieldsKey).(ContextFields); ok {
		return f
	}
	return ContextFields{}
}

// contextHandler оборачивает базовый slog.Handler и автоматически добавляет поля из ctx.
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
