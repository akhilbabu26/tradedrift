package middleware

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate" // Supports sustained request rates, Allows configurable bursts, Smoothly refills tokens over time, Doesn't require you to implement the algorithm yourself.
	"tradedrift/services/gateway/internal/response"
)

const (
	cleanupIntravel = time.Minute // how often to scan for stale entries
	staleThreshold = 3 * time.Minute // how long before an IP entry is evicted
)

type client struct{
	limiter *rate.Limiter
	lastSeen time.Time
}

// RateLimiter manages per-IP rate limiters with automatic stale-entry cleanup.
type RateLimiter struct{
	mu sync.Mutex
	clients map[string]*client
	limit rate.Limit
	burst int
}
// NewRateLimiter creates a per-IP rate
// Example — 5 requests per minute, no burst:
//
//	NewRateLimiter(ctx, rate.Every(time.Minute/5), 1)
func NewRateLimiter(ctx context.Context, limit rate.Limit, burst int) *RateLimiter{
	rl := &RateLimiter{
		clients: make(map[string]*client),
		limit: limit,
		burst: burst,
	}

	go rl.cleanup(ctx)
	return rl
}

func (rl *RateLimiter) getLimiter(ip string) *rate.Limiter{
	rl.mu.Lock()
	defer rl.mu.Unlock()

	c, ok := rl.clients[ip]
	if !ok {
		c = &client{limiter: rate.NewLimiter(rl.limit, rl.burst)}
		rl.clients[ip] = c
	}
	c.lastSeen = time.Now()
	return c.limiter
}

func (rl *RateLimiter) cleanup(ctx context.Context){
	ticker := time.NewTicker(cleanupIntravel)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			for ip, c := range rl.clients{
				if time.Since(c.lastSeen) > staleThreshold{
					delete(rl.clients, ip)
				}
			}
			rl.mu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}

// Middleware enforces per-IP rate limits using Allow() — allow now or reject immediately.
// NOTE: Uses r.RemoteAddr. Replace with trusted proxy header when behind nginx/Cloudflare.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler{
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request){
		ip := r.RemoteAddr
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil{
			ip = host
		}

		if !rl.getLimiter(ip).Allow(){
			response.WriteError(w, http.StatusTooManyRequests,
				"API_RATE_LIMIT_EXCEEDED", "rate limit exceeded, please try again later",
			)
			return
		}

		next.ServeHTTP(w, r)
	})
}