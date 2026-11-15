package ratelimit

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// bucket is a per-IP token bucket.
type bucket struct {
	mu         sync.Mutex
	tokens     float64
	lastRefill time.Time
}

// IPRateLimiter applies a token-bucket rate limiter keyed by client IP.
type IPRateLimiter struct {
	rate   float64 // tokens replenished per second
	burst  float64 // max tokens (burst capacity)
	mu     sync.Mutex
	limits map[string]*bucket
}

// New creates an IPRateLimiter.
// rate = tokens per second, burst = max burst tokens allowed.
func New(rate, burst float64) *IPRateLimiter {
	rl := &IPRateLimiter{
		rate:   rate,
		burst:  burst,
		limits: make(map[string]*bucket),
	}
	go rl.cleanup()
	return rl
}

// Allow checks whether the given IP is within the rate limit.
func (rl *IPRateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	b, ok := rl.limits[ip]
	if !ok {
		b = &bucket{tokens: rl.burst, lastRefill: time.Now()}
		rl.limits[ip] = b
	}
	rl.mu.Unlock()

	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens = b.tokens + elapsed*rl.rate
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	b.lastRefill = now

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// Middleware returns an http.Handler that enforces rate limiting.
// Requests exceeding the limit receive HTTP 429.
func (rl *IPRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !rl.Allow(ip) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limit exceeded, please slow down"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts the real client IP, honoring common proxy headers.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		// Take only the first (leftmost) IP from the comma-separated list.
		if idx := strings.Index(fwd, ","); idx != -1 {
			return strings.TrimSpace(fwd[:idx])
		}
		return strings.TrimSpace(fwd)
	}
	if real := r.Header.Get("X-Real-IP"); real != "" {
		return strings.TrimSpace(real)
	}
	// Strip the port from RemoteAddr ("ip:port" or "[ipv6]:port").
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}

// cleanup removes idle IP buckets every 5 minutes to prevent unbounded memory growth.
func (rl *IPRateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-10 * time.Minute)
		rl.mu.Lock()
		for ip, b := range rl.limits {
			b.mu.Lock()
			idle := b.lastRefill.Before(cutoff)
			b.mu.Unlock()
			if idle {
				delete(rl.limits, ip)
			}
		}
		rl.mu.Unlock()
	}
}
