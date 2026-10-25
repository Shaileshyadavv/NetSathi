package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"netsathi/internal/storage"
)

func main() {
	port := flag.Int("port", 8080, "HTTP server listening port")
	dataDir := flag.String("data", "./data", "Directory to store sqlite DB, chunks, and completed files")
	flag.Parse()

	// Ensure required directories exist
	chunkDir := filepath.Join(*dataDir, "chunks")
	completedDir := filepath.Join(*dataDir, "completed")
	if err := os.MkdirAll(chunkDir, 0755); err != nil {
		log.Fatalf("failed to create chunks dir: %v", err)
	}
	if err := os.MkdirAll(completedDir, 0755); err != nil {
		log.Fatalf("failed to create completed dir: %v", err)
	}

	// Initialize SQLite database
	dbPath := filepath.Join(*dataDir, "netsathi.db")
	db, err := storage.NewDB(dbPath)
	if err != nil {
		log.Fatalf("failed to initialize storage: %v", err)
	}
	defer db.Close()

	// Reconcile startup state (Module 3 & Section 11 of specification)
	reconciled, removed, err := db.ReconcileStartupState()
	if err != nil {
		log.Printf("[WARN] Startup reconciliation error: %v", err)
	} else {
		log.Printf("[INFO] Startup reconciliation: %d valid chunks verified, %d orphaned chunk records cleaned up", reconciled, removed)
	}

	// Root multiplexer
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","service":"NetSathi"}`))
	})

	serverAddr := fmt.Sprintf(":%d", *port)
	srv := &http.Server{
		Addr:         serverAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in background goroutine
	go func() {
		log.Printf("==================================================")
		log.Printf("  NetSathi File Transfer Server running on :%d", *port)
		log.Printf("  Data directory: %s", *dataDir)
		log.Printf("  Health check:   http://localhost:%d/healthz", *port)
		log.Printf("==================================================")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	log.Println("[INFO] Shutting down NetSathi server gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}
	log.Println("[INFO] Server exited cleanly.")
}
