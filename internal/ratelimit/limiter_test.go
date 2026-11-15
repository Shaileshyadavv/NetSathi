package ratelimit_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"netsathi/internal/ratelimit"
)

func TestAllow_BurstThenBlock(t *testing.T) {
	// burst=3, rate=0 means no replenishment — only burst tokens available
	rl := ratelimit.New(0, 3)
	ip := "192.168.1.1"

	// First 3 requests should be allowed (burst)
	for i := 0; i < 3; i++ {
		if !rl.Allow(ip) {
			t.Fatalf("request %d should be allowed within burst", i+1)
		}
	}

	// 4th request should be blocked
	if rl.Allow(ip) {
		t.Error("4th request should be blocked after burst exhausted")
	}
}

func TestAllow_TokenReplenishment(t *testing.T) {
	// rate=100 tokens/second, burst=1 — fast replenishment
	rl := ratelimit.New(100, 1)
	ip := "10.0.0.1"

	// Exhaust the single token
	if !rl.Allow(ip) {
		t.Fatal("first request should be allowed")
	}
	if rl.Allow(ip) {
		t.Fatal("second request should be blocked (burst=1)")
	}

	// Wait for replenishment (at 100/s, 1 token takes 10ms)
	time.Sleep(20 * time.Millisecond)

	if !rl.Allow(ip) {
		t.Error("request should be allowed after token replenishment")
	}
}

func TestAllow_DifferentIPs(t *testing.T) {
	// burst=1 per IP
	rl := ratelimit.New(0, 1)

	// Each IP gets its own bucket
	ips := []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}
	for _, ip := range ips {
		if !rl.Allow(ip) {
			t.Errorf("first request from %s should be allowed", ip)
		}
	}

	// Second request from each IP should be blocked
	for _, ip := range ips {
		if rl.Allow(ip) {
			t.Errorf("second request from %s should be blocked", ip)
		}
	}
}

func TestMiddleware_Returns429WhenLimitExceeded(t *testing.T) {
	// burst=1 — first request passes, second gets 429
	rl := ratelimit.New(0, 1)

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	makeReq := func() int {
		req := httptest.NewRequest("PUT", "/api/transfers/abc/chunks/0", nil)
		req.RemoteAddr = "192.168.0.1:12345"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr.Code
	}

	if code := makeReq(); code != http.StatusOK {
		t.Fatalf("expected 200 for first request, got %d", code)
	}
	if code := makeReq(); code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for rate-limited request, got %d", code)
	}
}

func TestMiddleware_AllowsAfterReplenishment(t *testing.T) {
	// rate=200/s, burst=1
	rl := ratelimit.New(200, 1)
	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	makeReq := func() int {
		req := httptest.NewRequest("PUT", "/", nil)
		req.RemoteAddr = "10.0.0.5:9999"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr.Code
	}

	makeReq() // consume the burst token
	if code := makeReq(); code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", code)
	}

	time.Sleep(10 * time.Millisecond) // wait for replenishment

	if code := makeReq(); code != http.StatusOK {
		t.Errorf("expected 200 after replenishment, got %d", code)
	}
}
