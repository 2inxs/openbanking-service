package mockbank

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/2inxs/openbanking-service/internal/mockbank/dto"
)

type AuthConfig struct {
	DemoToken    string
	DemoClientID string
}

type clientIDKey struct{}

func requireToken(auth AuthConfig, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(auth.DemoToken)) != 1 {
			writeUnauthorizedError(w)
			return
		}
		ctx := context.WithValue(r.Context(), clientIDKey{}, auth.DemoClientID)
		next(w, r.WithContext(ctx))
	}
}

func clientIDFrom(r *http.Request) string {
	id, _ := r.Context().Value(clientIDKey{}).(string)
	return id
}

func writeUnauthorizedError(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeError(w, http.StatusUnauthorized, dto.ErrCodeInvalidToken, "missing or invalid access token", "Authorization")
}
