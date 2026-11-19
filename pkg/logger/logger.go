package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"time"
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

func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	// Если любая из родительских групп является секретной (например, slog.Group("access_token", ...)),
	// скрываем все атрибуты внутри неё.
	for _, g := range groups {
		gk := strings.ToLower(strings.TrimSpace(g))
		if isSecretKey(gk) || isAmountKey(gk) {
			return slog.String(a.Key, redactedValue)
		}
	}

	if a.Value.Kind() == slog.KindGroup {
		attrs := a.Value.Group()
		nextGroups := append(append([]string(nil), groups...), a.Key)
		sanitized := make([]slog.Attr, len(attrs))
		for i, ga := range attrs {
			sanitized[i] = replaceAttr(nextGroups, ga)
		}
		return slog.Group(a.Key, anySlice(sanitized)...)
	}

	// Обрабатываем сложные объекты (map, slice, struct), переданные через slog.Any
	if a.Value.Kind() == slog.KindAny {
		key := strings.ToLower(strings.TrimSpace(a.Key))
		if isSecretKey(key) || isAmountKey(key) {
			return slog.String(a.Key, redactedValue)
		}
		return slog.Any(a.Key, sanitizeAny(a.Value.Any(), 0))
	}

	return sanitizeScalar(a)
}

func sanitizeScalar(a slog.Attr) slog.Attr {
	key := strings.ToLower(strings.TrimSpace(a.Key))

	switch {
	case isSecretKey(key) || isAmountKey(key):
		return slog.String(a.Key, redactedValue)
	case key == "email" || strings.HasSuffix(key, "_email"):
		return slog.String(a.Key, maskEmail(a.Value.String()))
	case isCardOrAccountKey(key):
		return slog.String(a.Key, maskLast4(a.Value.String()))
	default:
		return a
	}
}

func sanitizeAny(v any, depth int) any {
	if v == nil {
		return nil
	}
	if depth > 8 {
		return "[REDACTED]"
	}

	if err, ok := v.(error); ok {
		return err.Error()
	}

	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.Map:
		res := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			kStr := fmt.Sprint(iter.Key().Interface())
			res[kStr] = sanitizeFieldValue(kStr, iter.Value().Interface(), depth+1)
		}
		return res

	case reflect.Slice, reflect.Array:
		// []byte не трогаем
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return v
		}
		res := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			res[i] = sanitizeAny(rv.Index(i).Interface(), depth+1)
		}
		return res

	case reflect.Struct:
		// fmt.Stringer (например, time.Time) оставляем как есть
		if _, ok := v.(time.Time); ok {
			return v
		}
		rt := rv.Type()
		res := make(map[string]any, rt.NumField())
		for i := 0; i < rt.NumField(); i++ {
			field := rt.Field(i)
			if !field.IsExported() {
				continue
			}
			name := field.Name
			if tag := field.Tag.Get("json"); tag != "" {
				parts := strings.Split(tag, ",")
				if parts[0] == "-" {
					continue
				}
				if parts[0] != "" {
					name = parts[0]
				}
			}
			res[name] = sanitizeFieldValue(name, rv.Field(i).Interface(), depth+1)
		}
		return res

	default:
		return v
	}
}

func sanitizeFieldValue(key string, val any, depth int) any {
	k := strings.ToLower(strings.TrimSpace(key))
	switch {
	case isSecretKey(k) || isAmountKey(k):
		return redactedValue
	case k == "email" || strings.HasSuffix(k, "_email"):
		return maskEmail(fmt.Sprint(val))
	case isCardOrAccountKey(k):
		return maskLast4(fmt.Sprint(val))
	default:
		return sanitizeAny(val, depth)
	}
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

func isAmountKey(key string) bool {
	switch key {
	case "amount", "amount_kop", "balance", "balance_kop":
		return true
	default:
		return false
	}
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
	return string(runes[:1]) + "***" + email[at:]
}

func maskLast4(val string) string {
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
