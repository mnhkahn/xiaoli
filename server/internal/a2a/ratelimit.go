package a2a

import (
	"net/http"
	"sync"

	"golang.org/x/time/rate"
)

type RateLimitConfig struct {
	PerKeyLimit int // requests per minute
	GlobalLimit int // requests per minute
}

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rate.Limiter
	global  *rate.Limiter
	perKeyR rate.Limit
	globalR rate.Limit
}

func newRateLimiter(cfg RateLimitConfig) *rateLimiter {
	return &rateLimiter{
		buckets: make(map[string]*rate.Limiter),
		global:  rate.NewLimiter(rate.Limit(float64(cfg.GlobalLimit)/60.0), cfg.GlobalLimit),
		perKeyR: rate.Limit(float64(cfg.PerKeyLimit) / 60.0),
	}
}

func (rl *rateLimiter) getBucket(keyID string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	limiter, exists := rl.buckets[keyID]
	if !exists {
		// Convert per-minute limit to per-second rate
		// Burst = limit (allow full minute quota in burst)
		limiter = rate.NewLimiter(rl.perKeyR, int(rl.perKeyR*60))
		rl.buckets[keyID] = limiter
	}
	return limiter
}

// RateLimitMiddleware applies per-key and global rate limits using token bucket
func RateLimitMiddleware(cfg RateLimitConfig) func(http.Handler) http.Handler {
	rl := newRateLimiter(cfg)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check global limit first
			if !rl.global.Allow() {
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}

			// Check per-key limit
			keyID := GetKeyID(r)
			if keyID != "" {
				bucket := rl.getBucket(keyID)
				if !bucket.Allow() {
					http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}
