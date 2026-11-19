package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

const redactedValue = "[REDACTED]"

// New создаёт настроенный JSON-логгер с маскированием чувствительных данных.
// Если w == nil, вывод направляется в os.Stderr.
func New(levelStr string, w io.Writer) (*slog.Logger, error) {
	if w == nil {
		w = os.Stderr
	}

	level, err := parseLevel(levelStr)
	if err != nil {
		return nil, err
	}

	opts := &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: replaceAttr,
	}

	jsonHandler := slog.NewJSONHandler(w, opts)
	return slog.New(&contextHandler{next: jsonHandler}), nil
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("logger: invalid log level %q", s)
	}
}

func replaceAttr(_ []string, a slog.Attr) slog.Attr {
	// Рекурсивно обрабатываем вложенные группы атрибутов
	if a.Value.Kind() == slog.KindGroup {
		attrs := a.Value.Group()
		sanitized := make([]slog.Attr, len(attrs))
		for i, ga := range attrs {
			sanitized[i] = replaceAttr(nil, ga)
		}
		return slog.Group(a.Key, anySlice(sanitized)...)
	}

	key := strings.ToLower(strings.TrimSpace(a.Key))

	switch {
	// 1. Полное скрытие секретов, токенов, паролей и заголовков авторизации
	case isSecretKey(key):
		return slog.String(a.Key, redactedValue)

	// 2. По требованиям раздела 14.5 суммы операций не попадают в техлог
	case key == "amount" || key == "amount_kop" || key == "balance" || key == "balance_kop":
		return slog.String(a.Key, redactedValue)

	// 3. Маскирование email (оставляем первый символ и домен)
	case key == "email" || strings.HasSuffix(key, "_email"):
		return slog.String(a.Key, maskEmail(a.Value.String()))

	// 4. Маскирование номеров карт и счетов (оставляем только последние 4 цифры)
	case isCardOrAccountKey(key):
		return slog.String(a.Key, maskLast4(a.Value.String()))
	}

	return a
}

func isSecretKey(key string) bool {
	secretSubstrings := []string{
		"password",
		"passwd",
		"secret",
		"token",
		"authorization",
		"cookie",
		"cvv",
		"cvc",
		"api_key",
		"apikey",
		"private_key",
	}
	for _, sub := range secretSubstrings {
		if strings.Contains(key, sub) {
			return true
		}
	}
	return false
}

func isCardOrAccountKey(key string) bool {
	switch key {
	case "pan", "card_number", "card", "account_number", "account":
		return true
	default:
		return false
	}
}

func maskEmail(email string) string {
	email = strings.TrimSpace(email)
	if email == "" {
		return ""
	}
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return redactedValue
	}
	runes := []rune(email[:at])
	return string(runes[0]) + "***" + email[at:]
}

func maskLast4(val string) string {
	// Убираем пробелы и дефисы, чтобы корректно взять последние 4 цифры
	cleaned := strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(val))
	if cleaned == "" {
		return ""
	}
	runes := []rune(cleaned)
	if len(runes) <= 4 {
		return "****"
	}
	return "****" + string(runes[len(runes)-4:])
}

func anySlice(attrs []slog.Attr) []any {
	res := make([]any, len(attrs))
	for i, a := range attrs {
		res[i] = a
	}
	return res
}
