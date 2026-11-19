package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/2inxs/openbanking-service/pkg/logger"
)

type headerWrittenTracker interface {
	HeaderWritten() bool
}

// responseWriter оборачивает http.ResponseWriter для захвата кода статуса и размера ответа.
type responseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (rw *responseWriter) WriteHeader(code int) {
	// Промежуточные 1xx-ответы (например, 103 Early Hints) не завершают заголовки ответа.
	if code >= 100 && code < 200 {
		rw.ResponseWriter.WriteHeader(code)
		return
	}
	if rw.wroteHeader {
		return
	}
	rw.status = code
	rw.wroteHeader = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytes += n
	return n, err
}

func (rw *responseWriter) HeaderWritten() bool {
	return rw.wroteHeader
}

// Unwrap позволяет стандартным хелперам (например, http.ResponseController) добраться до исходного ResponseWriter.
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// Logging логирует метаданные каждого HTTP-запроса.
// В цепочке стоит после RequestID и перед Recover (RequestID -> Logging -> Recover),
// чтобы перехватывать итоговый статус 500, выставленный при панике.
func Logging(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Инициализируем состояние запроса, чтобы внутренние middleware (Auth)
			// могли записать userID для финального access-лога.
			ctx, reqState := logger.WithRequestState(r.Context())
			r = r.WithContext(ctx)

			rw := &responseWriter{
				ResponseWriter: w,
				status:         http.StatusOK,
			}

			defer func() {
				duration := time.Since(start)
				path := r.URL.Path

				logCtx := r.Context()
				if uid := reqState.UserID(); uid != "" {
					logCtx = logger.WithFields(logCtx, func(f *logger.ContextFields) {
						f.UserID = uid
					})
				}

				level := slog.LevelInfo
				switch {
				case rw.status >= 500:
					level = slog.LevelError
				case rw.status >= 400:
					level = slog.LevelWarn
				case path == "/healthz" || path == "/readyz" || path == "/health":
					level = slog.LevelDebug
				}

				log.LogAttrs(logCtx, level, "http request",
					slog.String("method", r.Method),
					slog.String("path", path),
					slog.Int("status", rw.status),
					slog.Int("bytes", rw.bytes),
					slog.Int64("duration_ms", duration.Milliseconds()),
					slog.String("remote_ip", clientIP(r)),
					slog.String("user_agent", r.UserAgent()),
				)
			}()

			next.ServeHTTP(rw, r)
		})
	}
}

func clientIP(r *http.Request) string {
	// Если сервис стоит за reverse-proxy, можно раскомментировать чтение X-Forwarded-For / X-Real-IP
	// после настройки списка доверенных прокси. Пока берём прямой RemoteAddr.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
