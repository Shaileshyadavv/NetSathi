package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"netsathi/internal/network"
	"netsathi/internal/ratelimit"
	"netsathi/internal/transfer"
)

// loggingMiddleware logs every incoming HTTP request with method, path, status, and duration.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &statusWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(lw, r)
		log.Printf("[HTTP] %s %s → %d (%s)", r.Method, r.URL.Path, lw.statusCode, time.Since(start).Round(time.Millisecond))
	})
}

// corsMiddleware adds CORS headers for browser-based dashboard access.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Chunk-SHA256")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// statusWriter wraps http.ResponseWriter to capture the status code for logging.
type statusWriter struct {
	http.ResponseWriter
	statusCode int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.statusCode = code
	sw.ResponseWriter.WriteHeader(code)
}

// Flush implements http.Flusher to support streaming responses (SSE).
func (sw *statusWriter) Flush() {
	if flusher, ok := sw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// NewRouter creates and configures the full HTTP mux with all API routes and middleware.
// limiter is optional; pass nil to disable rate limiting (e.g. in tests).
func NewRouter(mgr *transfer.Manager, webDir string, limiter ...*ratelimit.IPRateLimiter) http.Handler {
	handler := NewHandler(mgr)
	mux := http.NewServeMux()

	// Health check and LAN network discovery — no rate limiting needed
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		lanIP := network.GetOutboundIP()
		port := "8080"
		if idx := strings.LastIndex(r.Host, ":"); idx != -1 {
			port = r.Host[idx+1:]
		}
		lanURL := fmt.Sprintf("http://%s:%s", lanIP, port)

		json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"service": "NetSathi",
			"lan_ip":  lanIP,
			"lan_url": lanURL,
		})
	})

	// Build the API handler, optionally wrapped in a rate limiter
	var apiHandler http.Handler = http.HandlerFunc(handler.HandleTransfers)
	if len(limiter) > 0 && limiter[0] != nil {
		apiHandler = limiter[0].Middleware(apiHandler)
	}

	// API routes — single handler dispatches all /api/transfers/... paths
	mux.Handle("/api/transfers", apiHandler)
	mux.Handle("/api/transfers/", apiHandler)

	// Serve static web dashboard files
	if webDir != "" {
		fs := http.FileServer(http.Dir(webDir))
		mux.Handle("/", fs)
	}

	// Wrap with middleware: CORS → Logging → Routes
	return corsMiddleware(loggingMiddleware(mux))
}
