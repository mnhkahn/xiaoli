package a2a

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

// ServerConfig holds all configuration for the A2A server.
type ServerConfig struct {
	Auth                A2AConfig
	RateLimit           RateLimitConfig
	MaxInputChars       int
	TimeoutSeconds      int
	TaskTTLSeconds      int
	MaxConcurrentPerKey int
}

// NewServer assembles the full A2A HTTP handler.
// The a2a-go library owns the JSON-RPC transport and task store; this
// function wires in the key-partitioned task store, executor, and the
// auth / rate-limit / timeout middleware chain.
//
// Middleware order (outermost first): Auth → RateLimit → Timeout → JSON-RPC.
// Auth runs before RateLimit so unauthenticated requests do not consume
// per-key quota. Timeout bounds the total request lifetime including
// pipeline execution.
func NewServer(cfg ServerConfig, executor *Executor) http.Handler {
	ttl := time.Duration(cfg.TaskTTLSeconds) * time.Second
	store := newKeyPartitionedStore(ttl)

	requestHandler := a2asrv.NewHandler(executor, a2asrv.WithTaskStore(store))
	rpcHandler := a2asrv.NewJSONRPCHandler(requestHandler)

	handler := http.Handler(rpcHandler)
	handler = TimeoutMiddleware(cfg.TimeoutSeconds)(handler)
	if cfg.MaxConcurrentPerKey > 0 {
		// Concurrency must run after Auth to have key_id in context
		handler = ConcurrencyMiddleware(cfg.MaxConcurrentPerKey)(handler)
	}
	handler = RateLimitMiddleware(cfg.RateLimit)(handler)
	handler = AuthMiddleware(cfg.Auth)(handler)
	return handler
}

// ConcurrencyMiddleware limits the number of concurrent requests per API key.
// It uses a simple per-key semaphore pattern; keys exceeding the limit receive
// 429 Too Many Requests. The middleware must be placed after AuthMiddleware so
// that key_id is already in the request context.
func ConcurrencyMiddleware(maxPerKey int) func(http.Handler) http.Handler {
	type semaphore struct {
		count int
		ch    chan struct{}
	}
	var mu sync.Mutex
	semaphores := make(map[string]*semaphore)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			keyID := GetKeyID(r)
			if keyID == "" {
				next.ServeHTTP(w, r)
				return
			}

			mu.Lock()
			sem, exists := semaphores[keyID]
			if !exists {
				sem = &semaphore{ch: make(chan struct{}, maxPerKey)}
				semaphores[keyID] = sem
			}
			sem.count++
			mu.Unlock()

			select {
			case sem.ch <- struct{}{}:
				defer func() {
					<-sem.ch
					mu.Lock()
					sem.count--
					if sem.count == 0 {
						delete(semaphores, keyID)
					}
					mu.Unlock()
				}()
				next.ServeHTTP(w, r)
			default:
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				mu.Lock()
				sem.count--
				if sem.count == 0 {
					delete(semaphores, keyID)
				}
				mu.Unlock()
			}
		})
	}
}

// TimeoutMiddleware bounds the request lifetime. Zero or negative disables
// the timeout. On expiry the context is cancelled and the pipeline is
// expected to abort; the a2a-go library converts the aborted execution
// into a failed task.
func TimeoutMiddleware(seconds int) func(http.Handler) http.Handler {
	timeout := time.Duration(seconds) * time.Second
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if timeout <= 0 {
				next.ServeHTTP(w, r)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
