package api

import (
	"log"
	"net/http"
	"time"

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
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
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

// NewRouter creates and configures the full HTTP mux with all API routes and middleware.
func NewRouter(mgr *transfer.Manager, webDir string) http.Handler {
	handler := NewHandler(mgr)
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","service":"NetSathi"}`))
	})

	// API routes — single handler dispatches all /api/transfers/... paths
	mux.HandleFunc("/api/transfers", handler.HandleTransfers)
	mux.HandleFunc("/api/transfers/", handler.HandleTransfers)

	// Serve static web dashboard files
	if webDir != "" {
		fs := http.FileServer(http.Dir(webDir))
		mux.Handle("/", fs)
	}

	// Wrap with middleware: CORS → Logging → Routes
	return corsMiddleware(loggingMiddleware(mux))
}
