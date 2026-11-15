package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"netsathi/internal/integrity"
)

func printUsage() {
	fmt.Printf(`NetSathi CLI — Resumable & Integrity-Verified File Transfer Client

Usage:
  netsathi-cli <command> [arguments]

Commands:
  upload <filepath>            Upload a file with chunking and SHA-256 verification
  download <transfer-id>       Download a completed file
  list                         List recent transfers
  status <transfer-id>         Check progress and status of a transfer
  help                         Show this help message

Global Flags:
  -server string               Server URL (default: http://localhost:8080)
`)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "upload":
		cmdUpload(os.Args[2:])
	case "download":
		cmdDownload(os.Args[2:])
	case "list":
		cmdList(os.Args[2:])
	case "status":
		cmdStatus(os.Args[2:])
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\nRun 'netsathi-cli help' for usage.\n", command)
		os.Exit(1)
	}
}

// ============================================================
// UPLOAD COMMAND
// ============================================================

func cmdUpload(args []string) {
	fs := flag.NewFlagSet("upload", flag.ExitOnError)
	server := fs.String("server", "http://localhost:8080", "NetSathi server base URL")
	chunkSize := fs.Int64("chunk-size", 2*1024*1024, "Chunk size in bytes (1MB - 10MB)")
	concurrency := fs.Int("concurrency", 3, "Number of concurrent chunk upload streams")

	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: missing filepath to upload")
		fmt.Fprintln(os.Stderr, "Usage: netsathi-cli upload <filepath> [-server URL] [-chunk-size BYTES] [-concurrency N]")
		os.Exit(1)
	}

	filePath := fs.Arg(0)
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: file not found or inaccessible: %v\n", err)
		os.Exit(1)
	}

	fileSize := fileInfo.Size()
	fileName := filepath.Base(filePath)

	if fileSize <= 0 {
		fmt.Fprintln(os.Stderr, "Error: cannot upload empty file (0 bytes)")
		os.Exit(1)
	}

	fmt.Printf("📦 File:        %s\n", fileName)
	fmt.Printf("📊 Size:        %s (%d bytes)\n", formatBytes(fileSize), fileSize)
	fmt.Printf("⚙️  Chunk size:  %s\n", formatBytes(*chunkSize))
	fmt.Printf("⚡ Streams:     %d parallel\n", *concurrency)
	fmt.Print("⏳ Computing SHA-256... ")

	startHash := time.Now()
	hash, err := integrity.ComputeFileSHA256(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError computing SHA-256: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Done (%s)\n", time.Since(startHash).Round(time.Millisecond))
	fmt.Printf("🔒 SHA-256:     %s\n\n", hash)

	// 1. Create transfer on server
	fmt.Println("🚀 Initiating transfer session with server...")
	createPayload, _ := json.Marshal(map[string]any{
		"original_name":   fileName,
		"total_size":      fileSize,
		"chunk_size":      *chunkSize,
		"expected_sha256": hash,
	})

	apiBase := strings.TrimRight(*server, "/") + "/api/transfers"
	resp, err := http.Post(apiBase, "application/json", bytes.NewReader(createPayload))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error contacting server: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		var errData map[string]string
		json.NewDecoder(resp.Body).Decode(&errData)
		fmt.Fprintf(os.Stderr, "Server rejected transfer creation: %s\n", errData["error"])
		os.Exit(1)
	}

	var session struct {
		ID          string `json:"id"`
		Token       string `json:"token"`
		TotalChunks int    `json:"total_chunks"`
		ChunkSize   int64  `json:"chunk_size"`
	}
	json.NewDecoder(resp.Body).Decode(&session)

	fmt.Printf("✓ Session ID:   %s\n", session.ID)
	fmt.Printf("✓ Total Chunks: %d\n\n", session.TotalChunks)

	// 2. Query missing chunks (enables automatic resume)
	missingReq, _ := http.NewRequest("GET", fmt.Sprintf("%s/%s/missing", apiBase, session.ID), nil)
	missingReq.Header.Set("Authorization", "Bearer "+session.Token)
	missingResp, err := http.DefaultClient.Do(missingReq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error querying missing chunks: %v\n", err)
		os.Exit(1)
	}
	defer missingResp.Body.Close()

	var missingData struct {
		MissingChunks []int `json:"missing_chunks"`
	}
	json.NewDecoder(missingResp.Body).Decode(&missingData)

	missing := missingData.MissingChunks
	if len(missing) < session.TotalChunks {
		fmt.Printf("ℹ️ Resuming upload: %d/%d chunks already on server\n", session.TotalChunks-len(missing), session.TotalChunks)
	}

	// 3. Upload missing chunks using worker pool
	chunkQueue := make(chan int, len(missing))
	for _, idx := range missing {
		chunkQueue <- idx
	}
	close(chunkQueue)

	var (
		uploadedCount int64 = int64(session.TotalChunks - len(missing))
		bytesSent     int64
		startTime           = time.Now()
		wg            sync.WaitGroup
		uploadErr     error
		errMu         sync.Mutex
	)

	workerCount := *concurrency
	if workerCount > len(missing) {
		workerCount = len(missing)
	}
	if workerCount < 1 {
		workerCount = 1
	}

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Each worker opens its own file handle for concurrent random-access reads
			file, err := os.Open(filePath)
			if err != nil {
				errMu.Lock()
				uploadErr = err
				errMu.Unlock()
				return
			}
			defer file.Close()

			for chunkIdx := range chunkQueue {
				errMu.Lock()
				hasErr := uploadErr != nil
				errMu.Unlock()
				if hasErr {
					return
				}

				startOffset := int64(chunkIdx) * session.ChunkSize
				currChunkSize := session.ChunkSize
				if startOffset+currChunkSize > fileSize {
					currChunkSize = fileSize - startOffset
				}

				chunkBytes := make([]byte, currChunkSize)
				_, err = file.ReadAt(chunkBytes, startOffset)
				if err != nil && err != io.EOF {
					errMu.Lock()
					uploadErr = fmt.Errorf("read chunk %d: %w", chunkIdx, err)
					errMu.Unlock()
					return
				}

				// Compute chunk-level SHA-256
				cHash := sha256.Sum256(chunkBytes)
				cHashHex := hex.EncodeToString(cHash[:])

				chunkURL := fmt.Sprintf("%s/%s/chunks/%d", apiBase, session.ID, chunkIdx)
				putReq, _ := http.NewRequest("PUT", chunkURL, bytes.NewReader(chunkBytes))
				putReq.Header.Set("Authorization", "Bearer "+session.Token)
				putReq.Header.Set("X-Chunk-SHA256", cHashHex)

				putResp, err := http.DefaultClient.Do(putReq)
				if err != nil {
					errMu.Lock()
					uploadErr = fmt.Errorf("upload chunk %d: %w", chunkIdx, err)
					errMu.Unlock()
					return
				}
				putResp.Body.Close()

				if putResp.StatusCode != http.StatusOK {
					errMu.Lock()
					uploadErr = fmt.Errorf("chunk %d upload returned HTTP %d", chunkIdx, putResp.StatusCode)
					errMu.Unlock()
					return
				}

				atomic.AddInt64(&uploadedCount, 1)
				atomic.AddInt64(&bytesSent, currChunkSize)

				// Progress bar render
				currDone := atomic.LoadInt64(&uploadedCount)
				pct := float64(currDone) / float64(session.TotalChunks) * 100
				elapsed := time.Since(startTime).Seconds()
				var speedStr string
				if elapsed > 0.05 {
					speedStr = formatSpeed(float64(atomic.LoadInt64(&bytesSent)) / elapsed)
				} else {
					speedStr = "--"
				}

				renderProgressBar(int(pct), currDone, int64(session.TotalChunks), speedStr)
			}
		}()
	}

	wg.Wait()

	if uploadErr != nil {
		fmt.Printf("\n❌ Upload failed: %v\n", uploadErr)
		fmt.Println("Tip: Run the same upload command again to resume from the last confirmed chunk.")
		os.Exit(1)
	}

	fmt.Printf("\n⏳ Assembling chunks and verifying SHA-256 on server...\n")

	// 4. Complete and verify
	compReq, _ := http.NewRequest("POST", fmt.Sprintf("%s/%s/complete", apiBase, session.ID), nil)
	compReq.Header.Set("Authorization", "Bearer "+session.Token)
	compResp, err := http.DefaultClient.Do(compReq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error triggering completion: %v\n", err)
		os.Exit(1)
	}
	defer compResp.Body.Close()

	if compResp.StatusCode != http.StatusOK {
		var errData map[string]string
		json.NewDecoder(compResp.Body).Decode(&errData)
		fmt.Fprintf(os.Stderr, "❌ Verification failed: %s\n", errData["error"])
		os.Exit(1)
	}

	duration := time.Since(startTime).Round(time.Millisecond)
	avgSpeed := float64(fileSize) / time.Since(startTime).Seconds()

	fmt.Println("==================================================")
	fmt.Println("🎉 TRANSFER COMPLETED AND VERIFIED!")
	fmt.Printf("   File:        %s\n", fileName)
	fmt.Printf("   SHA-256:     %s\n", hash)
	fmt.Printf("   Time:        %s (avg %s)\n", duration, formatSpeed(avgSpeed))
	fmt.Printf("   Download:    %s/%s/download\n", apiBase, session.ID)
	fmt.Println("==================================================")
}

// ============================================================
// DOWNLOAD COMMAND
// ============================================================

func cmdDownload(args []string) {
	fs := flag.NewFlagSet("download", flag.ExitOnError)
	server := fs.String("server", "http://localhost:8080", "NetSathi server base URL")
	output := fs.String("output", "", "Output filename / path (default: original server filename)")

	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: missing transfer ID")
		fmt.Fprintln(os.Stderr, "Usage: netsathi-cli download <transfer-id> [-server URL] [-output PATH]")
		os.Exit(1)
	}

	transferID := fs.Arg(0)
	dlURL := fmt.Sprintf("%s/api/transfers/%s/download", strings.TrimRight(*server, "/"), transferID)

	fmt.Printf("📥 Downloading from: %s\n", dlURL)

	resp, err := http.Get(dlURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to server: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errData map[string]string
		json.NewDecoder(resp.Body).Decode(&errData)
		fmt.Fprintf(os.Stderr, "Download rejected (HTTP %d): %s\n", resp.StatusCode, errData["error"])
		os.Exit(1)
	}

	// Determine output filename
	targetPath := *output
	if targetPath == "" {
		cd := resp.Header.Get("Content-Disposition")
		if strings.Contains(cd, "filename=") {
			parts := strings.Split(cd, "filename=")
			if len(parts) > 1 {
				targetPath = strings.Trim(parts[1], `"`)
			}
		}
	}
	if targetPath == "" {
		targetPath = "downloaded_" + transferID
	}

	out, err := os.Create(targetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create output file: %v\n", err)
		os.Exit(1)
	}
	defer out.Close()

	written, err := io.Copy(out, resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing downloaded content: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Saved %s (%s) to %s\n", formatBytes(written), targetPath, targetPath)
}

// ============================================================
// LIST COMMAND
// ============================================================

func cmdList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	server := fs.String("server", "http://localhost:8080", "NetSathi server base URL")
	limit := fs.Int("limit", 20, "Max transfers to display")

	fs.Parse(args)

	url := fmt.Sprintf("%s/api/transfers?limit=%d", strings.TrimRight(*server, "/"), *limit)
	resp, err := http.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to reach server: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var data struct {
		Transfers []struct {
			ID           string `json:"id"`
			OriginalName string `json:"original_name"`
			TotalSize    int64  `json:"total_size"`
			TotalChunks  int    `json:"total_chunks"`
			Status       string `json:"status"`
			ActualSHA256 string `json:"actual_sha256"`
			UpdatedAt    string `json:"updated_at"`
		} `json:"transfers"`
		Count int `json:"count"`
	}
	json.NewDecoder(resp.Body).Decode(&data)

	if len(data.Transfers) == 0 {
		fmt.Println("No transfers found on server.")
		return
	}

	fmt.Printf("%-36s  %-20s  %-10s  %-8s  %-12s  %s\n", "TRANSFER ID", "FILE NAME", "SIZE", "CHUNKS", "STATUS", "SHA-256 (PREFIX)")
	fmt.Println(strings.Repeat("-", 105))
	for _, t := range data.Transfers {
		hashPrefix := "N/A"
		if len(t.ActualSHA256) >= 12 {
			hashPrefix = t.ActualSHA256[:12] + "..."
		}
		name := t.OriginalName
		if len(name) > 20 {
			name = name[:17] + "..."
		}
		fmt.Printf("%-36s  %-20s  %-10s  %-8d  %-12s  %s\n",
			t.ID, name, formatBytes(t.TotalSize), t.TotalChunks, t.Status, hashPrefix)
	}
}

// ============================================================
// STATUS COMMAND
// ============================================================

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	server := fs.String("server", "http://localhost:8080", "NetSathi server base URL")

	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: missing transfer ID")
		fmt.Fprintln(os.Stderr, "Usage: netsathi-cli status <transfer-id> [-server URL]")
		os.Exit(1)
	}

	transferID := fs.Arg(0)
	url := fmt.Sprintf("%s/api/transfers/%s", strings.TrimRight(*server, "/"), transferID)
	resp, err := http.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to reach server: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Transfer %s not found (HTTP %d)\n", transferID, resp.StatusCode)
		os.Exit(1)
	}

	var data struct {
		Transfer struct {
			ID             string `json:"id"`
			OriginalName   string `json:"original_name"`
			TotalSize      int64  `json:"total_size"`
			ChunkSize      int64  `json:"chunk_size"`
			TotalChunks    int    `json:"total_chunks"`
			ExpectedSHA256 string `json:"expected_sha256"`
			ActualSHA256   string `json:"actual_sha256"`
			Status         string `json:"status"`
			UpdatedAt      string `json:"updated_at"`
		} `json:"transfer"`
		ReceivedChunks int     `json:"received_chunks"`
		ReceivedBytes  int64   `json:"received_bytes"`
		Percentage     float64 `json:"percentage"`
	}
	json.NewDecoder(resp.Body).Decode(&data)

	fmt.Println("==================================================")
	fmt.Printf("Transfer ID:     %s\n", data.Transfer.ID)
	fmt.Printf("File Name:       %s\n", data.Transfer.OriginalName)
	fmt.Printf("Total Size:      %s (%d bytes)\n", formatBytes(data.Transfer.TotalSize), data.Transfer.TotalSize)
	fmt.Printf("Progress:        %.1f%% (%d / %d chunks)\n", data.Percentage, data.ReceivedChunks, data.Transfer.TotalChunks)
	fmt.Printf("Status:          %s\n", data.Transfer.Status)
	fmt.Printf("Expected SHA:    %s\n", data.Transfer.ExpectedSHA256)
	if data.Transfer.ActualSHA256 != "" {
		fmt.Printf("Actual SHA:      %s\n", data.Transfer.ActualSHA256)
	}
	fmt.Printf("Last Updated:    %s\n", data.Transfer.UpdatedAt)
	fmt.Println("==================================================")
}

// ============================================================
// HELPERS
// ============================================================

func renderProgressBar(pct int, done, total int64, speed string) {
	barWidth := 25
	filled := (pct * barWidth) / 100
	if filled > barWidth {
		filled = barWidth
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
	fmt.Printf("\r[%s] %3d%%  (%d/%d chunks)  ⚡ %s   ", bar, pct, done, total, speed)
}

func formatBytes(bytes int64) string {
	if bytes <= 0 {
		return "0 B"
	}
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func formatSpeed(bytesPerSec float64) string {
	if bytesPerSec <= 0 {
		return "0 B/s"
	}
	if bytesPerSec < 1024 {
		return fmt.Sprintf("%.0f B/s", bytesPerSec)
	}
	if bytesPerSec < 1024*1024 {
		return fmt.Sprintf("%.1f KB/s", bytesPerSec/1024)
	}
	return fmt.Sprintf("%.2f MB/s", bytesPerSec/(1024*1024))
}

