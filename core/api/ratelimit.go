package api

import (
	"context"
	"hash/fnv"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const rateLimiterShards = 16

type limiterBucket struct {
	limiter    *rate.Limiter
	lastAccess time.Time
}

type limiterShard struct {
	mu       sync.Mutex
	limiters map[string]*limiterBucket
}

type RateLimiter struct {
	shards [rateLimiterShards]*limiterShard
	rate   rate.Limit
	burst  int
	cancel context.CancelFunc
}

func NewRateLimiter(ctx context.Context, r float64, b int) *RateLimiter {
	ctx, cancel := context.WithCancel(ctx)
	rl := &RateLimiter{
		rate:   rate.Limit(r),
		burst:  b,
		cancel: cancel,
	}
	for i := range rl.shards {
		rl.shards[i] = &limiterShard{
			limiters: make(map[string]*limiterBucket),
		}
	}

	go rl.cleanup(ctx)
	return rl
}

func (rl *RateLimiter) Stop() {
	rl.cancel()
}

func (rl *RateLimiter) shardIdx(subject string) int {
	h := fnv.New32a()
	h.Write([]byte(subject))
	return int(h.Sum32()) % rateLimiterShards
}

func (rl *RateLimiter) cleanup(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			for _, sh := range rl.shards {
				sh.mu.Lock()
				for sub, b := range sh.limiters {
					if now.Sub(b.lastAccess) > 5*time.Minute {
						delete(sh.limiters, sub)
					}
				}
				sh.mu.Unlock()
			}
		case <-ctx.Done():
			return
		}
	}
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value(ClaimsKey).(*Claims)
		if !ok || claims == nil || claims.Subject == "" {
			next.ServeHTTP(w, r)
			return
		}

		sh := rl.shards[rl.shardIdx(claims.Subject)]
		sh.mu.Lock()
		b, ok := sh.limiters[claims.Subject]
		if !ok {
			b = &limiterBucket{
				limiter: rate.NewLimiter(rl.rate, rl.burst),
			}
			sh.limiters[claims.Subject] = b
		}
		b.lastAccess = time.Now()
		sh.mu.Unlock()

		if !b.limiter.Allow() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
