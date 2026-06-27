package a2a

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
)

type contextKey string

const keyIDContextKey contextKey = "a2a_key_id"

// A2AConfig is a local copy of config fields needed for auth
type A2AConfig struct {
	APIKeys map[string]string // key_id -> secret
}

// AuthMiddleware validates Bearer token using constant-time comparison
func AuthMiddleware(cfg A2AConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if auth == "" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			parts := strings.SplitN(auth, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			token := parts[1]
			keyID := validateToken(cfg.APIKeys, token)
			if keyID == "" {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			ctx := context.WithValue(r.Context(), keyIDContextKey, keyID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetKeyID extracts the authenticated key_id from request context
func GetKeyID(r *http.Request) string {
	if keyID, ok := r.Context().Value(keyIDContextKey).(string); ok {
		return keyID
	}
	return ""
}

// validateToken uses constant-time comparison to prevent timing attacks
// Returns matching key_id or empty string if not found
func validateToken(keys map[string]string, token string) string {
	for keyID, secret := range keys {
		if subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1 {
			return keyID
		}
	}
	return ""
}
