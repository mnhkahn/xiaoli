package a2a

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAuthMiddleware_ValidToken(t *testing.T) {
	cfg := A2AConfig{
		APIKeys: map[string]string{"partner_a": "secret123"},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keyID := GetKeyID(r)
		assert.Equal(t, "partner_a", keyID)
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/a2a", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	rr := httptest.NewRecorder()

	AuthMiddleware(cfg)(handler).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestAuthMiddleware_MissingToken(t *testing.T) {
	cfg := A2AConfig{APIKeys: map[string]string{"a": "sec"}}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})

	req := httptest.NewRequest("POST", "/a2a", nil)
	rr := httptest.NewRecorder()

	AuthMiddleware(cfg)(handler).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	cfg := A2AConfig{APIKeys: map[string]string{"a": "secret"}}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})

	req := httptest.NewRequest("POST", "/a2a", nil)
	req.Header.Set("Authorization", "Bearer wrong_secret")
	rr := httptest.NewRecorder()

	AuthMiddleware(cfg)(handler).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestAuthMiddleware_EmptyAPIKeys(t *testing.T) {
	cfg := A2AConfig{APIKeys: map[string]string{}}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})

	req := httptest.NewRequest("POST", "/a2a", nil)
	req.Header.Set("Authorization", "Bearer any")
	rr := httptest.NewRecorder()

	AuthMiddleware(cfg)(handler).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}
