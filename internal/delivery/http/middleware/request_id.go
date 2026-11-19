package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/2inxs/openbanking-service/pkg/logger"
)

const (
	HeaderRequestID     = "X-Request-Id"
	HeaderInteractionID = "x-fapi-interaction-id"
)

// RequestID извлекает или генерирует идентификаторы запроса и кладёт их в контекст логгера.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get(HeaderRequestID)
		if reqID == "" {
			reqID = generateID()
		}

		interactionID := r.Header.Get(HeaderInteractionID)

		w.Header().Set(HeaderRequestID, reqID)
		if interactionID != "" {
			w.Header().Set(HeaderInteractionID, interactionID)
		}

		ctx := logger.WithFields(r.Context(), func(f *logger.ContextFields) {
			f.RequestID = reqID
			f.InteractionID = interactionID
		})

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "fallback-id"
	}
	return hex.EncodeToString(b)
}
