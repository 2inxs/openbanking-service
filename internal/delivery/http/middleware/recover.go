package middleware

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/2inxs/openbanking-service/pkg/logger"
)

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Recover перехватывает паники в нижележащих хендлерах, пишет стек в лог
// и возвращает клиенту безопасный ответ 500 Internal Server Error.
// В цепочке должен стоять после RequestID и Logging.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Если выше в цепочке нет Logging middleware, оборачиваем ResponseWriter сами,
			// чтобы всё равно отслеживать факт отправки заголовков.
			if _, ok := findHeaderTracker(w); !ok {
				w = &responseWriter{
					ResponseWriter: w,
					status:         http.StatusOK,
				}
			}

			defer func() {
				rec := recover()
				if rec == nil {
					return
				}

				// http.ErrAbortHandler используется net/http для принудительного обрыва соединения,
				// её перехватывать и глушить нельзя.
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}

				stack := string(debug.Stack())
				reqID := logger.FieldsFromContext(r.Context()).RequestID

				log.ErrorContext(r.Context(), "panic recovered",
					slog.String("panic", fmt.Sprintf("%v", rec)),
					slog.String("stack", stack),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
				)

				// Если заголовки уже ушли клиенту, менять статус и дописывать JSON нельзя —
				// сигнализируем net/http о необходимости принудительно оборвать ответ.
				if tracker, ok := findHeaderTracker(w); ok && tracker.HeaderWritten() {
					panic(http.ErrAbortHandler)
				}

				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				if reqID != "" {
					w.Header().Set(HeaderRequestID, reqID)
				}
				w.WriteHeader(http.StatusInternalServerError)

				_ = json.NewEncoder(w).Encode(errorResponse{
					Code:    "internal.error",
					Message: "Внутренняя ошибка сервера",
				})
			}()

			next.ServeHTTP(w, r)
		})
	}
}

func findHeaderTracker(w http.ResponseWriter) (headerWrittenTracker, bool) {
	for w != nil {
		if t, ok := w.(headerWrittenTracker); ok {
			return t, true
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		w = u.Unwrap()
	}
	return nil, false
}
