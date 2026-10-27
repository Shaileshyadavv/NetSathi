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

	"netsathi/internal/api"
	"netsathi/internal/storage"
	"netsathi/internal/transfer"
)

func main() {
	port := flag.Int("port", 8080, "HTTP server listening port")
	dataDir := flag.String("data", "./data", "Directory to store sqlite DB, chunks, and completed files")
	webDir := flag.String("web", "./web", "Directory containing the static web dashboard files")
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

	// Reconcile startup state (crash recovery)
	reconciled, removed, err := db.ReconcileStartupState()
	if err != nil {
		log.Printf("[WARN] Startup reconciliation error: %v", err)
	} else {
		log.Printf("[INFO] Startup reconciliation: %d valid chunks verified, %d orphaned chunk records cleaned up", reconciled, removed)
	}

	// Initialize the transfer manager
	mgr := transfer.NewManager(db, chunkDir, completedDir)

	// Start automatic cleanup worker (Page 3 optional extension: purges incomplete transfers older than 24h)
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	defer cleanupCancel()
	mgr.StartCleanupWorker(cleanupCtx, 1*time.Hour, 24*time.Hour)

	// Build the full HTTP router with all API endpoints and middleware
	router := api.NewRouter(mgr, *webDir)

	serverAddr := fmt.Sprintf(":%d", *port)
	srv := &http.Server{
		Addr:         serverAddr,
		Handler:      router,
		ReadTimeout:  5 * time.Minute, // Allow large chunk uploads
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in background goroutine
	go func() {
		log.Printf("==================================================")
		log.Printf("  NetSathi File Transfer Server")
		log.Printf("  Port:         %d", *port)
		log.Printf("  Data dir:     %s", *dataDir)
		log.Printf("  Web dir:      %s", *webDir)
		log.Printf("  Health:       http://localhost:%d/healthz", *port)
		log.Printf("  API Base:     http://localhost:%d/api/transfers", *port)
		log.Printf("  Dashboard:    http://localhost:%d/", *port)
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}
	log.Println("[INFO] Server exited cleanly.")
}
