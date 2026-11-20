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

// reservedContextKeys защищены от повторного маскирования, так как содержат системные UUID/ID.
var reservedContextKeys = map[string]struct{}{
	"request_id":     {},
	"trace_id":       {},
	"user_id":        {},
	"job":            {},
	"bank_id":        {},
	"interaction_id": {},
	"resource_id":    {},
}

func isReservedSystemIDKey(key string) bool {
	_, ok := reservedContextKeys[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

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
	for _, g := range groups {
		gk := strings.ToLower(strings.TrimSpace(g))
		if IsSecretKey(gk) || IsAmountKey(gk) {
			return slog.String(a.Key, RedactedValue)
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

	if a.Value.Kind() == slog.KindAny {
		key := strings.ToLower(strings.TrimSpace(a.Key))
		if IsSecretKey(key) || IsAmountKey(key) {
			return slog.String(a.Key, RedactedValue)
		}
		val := a.Value.Any()
		if isReservedSystemIDKey(key) {
			if s, ok := val.(string); ok {
				return slog.String(a.Key, s)
			}
		}
		return slog.Any(a.Key, sanitizeAny(val, 0))
	}

	return sanitizeScalar(a)
}

func sanitizeScalar(a slog.Attr) slog.Attr {
	key := strings.ToLower(strings.TrimSpace(a.Key))

	switch {
	case IsSecretKey(key) || IsAmountKey(key):
		return slog.String(a.Key, RedactedValue)
	case key == "email" || strings.HasSuffix(key, "_email"):
		return slog.String(a.Key, MaskEmail(a.Value.String()))
	case IsCardOrAccountKey(key):
		return slog.String(a.Key, MaskLast4(a.Value.String()))
	case a.Value.Kind() == slog.KindString:
		if isReservedSystemIDKey(key) {
			return a
		}
		return slog.String(a.Key, SanitizeStringValue(a.Value.String()))
	default:
		return a
	}
}

func sanitizeAny(v any, depth int) any {
	if v == nil {
		return nil
	}
	if depth > 8 {
		return RedactedValue
	}

	if err, ok := v.(error); ok {
		return SanitizeStringValue(err.Error())
	}

	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.String:
		return SanitizeStringValue(rv.String())

	case reflect.Map:
		res := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			kStr := fmt.Sprint(iter.Key().Interface())
			res[kStr] = sanitizeFieldValue(kStr, iter.Value().Interface(), depth+1)
		}
		return res

	case reflect.Slice, reflect.Array:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return v
		}
		res := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			res[i] = sanitizeAny(rv.Index(i).Interface(), depth+1)
		}
		return res

	case reflect.Struct:
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
	case IsSecretKey(k) || IsAmountKey(k):
		return RedactedValue
	case k == "email" || strings.HasSuffix(k, "_email"):
		return MaskEmail(fmt.Sprint(val))
	case IsCardOrAccountKey(k):
		return MaskLast4(fmt.Sprint(val))
	case isReservedSystemIDKey(k):
		if s, ok := val.(string); ok {
			return s
		}
		return sanitizeAny(val, depth)
	default:
		return sanitizeAny(val, depth)
	}
}

func anySlice(attrs []slog.Attr) []any {
	res := make([]any, len(attrs))
	for i, a := range attrs {
		res[i] = a
	}
	return res
}
