package a2a

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRateLimitMiddleware_PerKeyLimit(t *testing.T) {
	cfg := RateLimitConfig{
		PerKeyLimit: 2, // 2 per minute
		GlobalLimit: 100,
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := RateLimitMiddleware(cfg)(handler)

	req := httptest.NewRequest("POST", "/a2a", nil)
	ctx := context.WithValue(req.Context(), keyIDContextKey, "partner_a")
	req = req.WithContext(ctx)

	// First 2 should pass
	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		middleware.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code, "request %d should pass", i+1)
	}

	// 3rd should be limited
	rr := httptest.NewRecorder()
	middleware.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusTooManyRequests, rr.Code)
}

func TestRateLimitMiddleware_GlobalLimit(t *testing.T) {
	cfg := RateLimitConfig{
		PerKeyLimit: 10,
		GlobalLimit: 2, // Only 2 global per minute
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := RateLimitMiddleware(cfg)(handler)

	// Request from key1
	req1 := httptest.NewRequest("POST", "/a2a", nil)
	ctx1 := context.WithValue(req1.Context(), keyIDContextKey, "key1")
	req1 = req1.WithContext(ctx1)

	// Request from key2
	req2 := httptest.NewRequest("POST", "/a2a", nil)
	ctx2 := context.WithValue(req2.Context(), keyIDContextKey, "key2")
	req2 = req2.WithContext(ctx2)

	// First 2 pass (regardless of key)
	rr := httptest.NewRecorder()
	middleware.ServeHTTP(rr, req1)
	assert.Equal(t, http.StatusOK, rr.Code)

	rr = httptest.NewRecorder()
	middleware.ServeHTTP(rr, req2)
	assert.Equal(t, http.StatusOK, rr.Code)

	// 3rd from either key hits global limit
	rr = httptest.NewRecorder()
	middleware.ServeHTTP(rr, req1)
	assert.Equal(t, http.StatusTooManyRequests, rr.Code)
}

func TestRateLimitMiddleware_NoKeyID(t *testing.T) {
	cfg := RateLimitConfig{PerKeyLimit: 10, GlobalLimit: 10}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := RateLimitMiddleware(cfg)(handler)

	// Request without key_id (shouldn't happen in practice after auth, but handled)
	req := httptest.NewRequest("POST", "/a2a", nil)
	rr := httptest.NewRecorder()
	middleware.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code) // passes global limit check
}
