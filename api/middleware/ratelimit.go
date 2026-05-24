package middleware

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/mitsuakki/minestrate/api/service"
	"golang.org/x/time/rate"
)

type limiterBucket struct {
	limiter    *rate.Limiter
	lastAccess time.Time
}

type RateLimiter struct {
	limiters map[string]*limiterBucket
	mu       sync.Mutex
	rate     rate.Limit
	burst    int
	cancel   context.CancelFunc
}

func NewRateLimiter(ctx context.Context, r float64, b int) *RateLimiter {
	ctx, cancel := context.WithCancel(ctx)
	rl := &RateLimiter{
		limiters: make(map[string]*limiterBucket),
		rate:     rate.Limit(r),
		burst:    b,
		cancel:   cancel,
	}

	go rl.cleanup(ctx)
	return rl
}

func (rl *RateLimiter) Stop() {
	rl.cancel()
}

func (rl *RateLimiter) cleanup(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for sub, b := range rl.limiters {
				if now.Sub(b.lastAccess) > 5*time.Minute {
					delete(rl.limiters, sub)
				}
			}
			rl.mu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value(ClaimsKey).(*service.Claims)
		if !ok || claims.Subject == "" {
			next.ServeHTTP(w, r)
			return
		}

		rl.mu.Lock()
		b, ok := rl.limiters[claims.Subject]
		if !ok {
			b = &limiterBucket{
				limiter: rate.NewLimiter(rl.rate, rl.burst),
			}
			rl.limiters[claims.Subject] = b
		}
		b.lastAccess = time.Now()
		rl.mu.Unlock()

		if !b.limiter.Allow() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
