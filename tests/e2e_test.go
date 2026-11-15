package tests

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"netsathi/internal/api"
	"netsathi/internal/storage"
	"netsathi/internal/transfer"
)

// TestEndToEndNetworkInterruptionAndRecovery simulates:
// 1. Client creates a 5-chunk transfer session
// 2. Client uploads chunk 0 and chunk 1
// 3. Simulated Network Drop / Client crash
// 4. Client queries /missing -> verifies server says chunks [2, 3, 4] are missing
// 5. Simulated Server Restart: DB re-opens, ReconcileStartupState runs
// 6. Client resumes by sending only missing chunks 2, 3, 4
// 7. Client triggers /complete -> server verifies SHA-256
// 8. Client downloads file -> bit-for-bit comparison with original file
func TestEndToEndNetworkInterruptionAndRecovery(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "netsathi_e2e.db")
	chunkDir := filepath.Join(tempDir, "chunks")
	completedDir := filepath.Join(tempDir, "completed")
	webDir := filepath.Join(tempDir, "web")

	os.MkdirAll(chunkDir, 0755)
	os.MkdirAll(completedDir, 0755)
	os.MkdirAll(webDir, 0755)

	// Step A: Generate a pseudo-random 5-chunk file (5 x 512 bytes = 2560 bytes)
	fileData := make([]byte, 2560)
	for i := range fileData {
		fileData[i] = byte((i * 31) % 256)
	}
	wholeHash := sha256.Sum256(fileData)
	expectedSHA256 := hex.EncodeToString(wholeHash[:])
	chunkSize := 512

	// Step B: Start initial server instance
	db1, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	mgr1 := transfer.NewManager(db1, chunkDir, completedDir)
	router1 := api.NewRouter(mgr1, webDir)

	// 1. Create transfer
	createPayload, _ := json.Marshal(map[string]any{
		"original_name":   "e2e_sample_data.bin",
		"total_size":      len(fileData),
		"chunk_size":      chunkSize,
		"expected_sha256": expectedSHA256,
	})

	req := httptest.NewRequest("POST", "/api/transfers", bytes.NewReader(createPayload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router1.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("failed to create transfer: %d %s", w.Code, w.Body.String())
	}

	var createResp map[string]any
	json.NewDecoder(w.Body).Decode(&createResp)
	transferID := createResp["id"].(string)
	token := createResp["token"].(string)

	// 2. Upload only chunk 0 and chunk 1
	for idx := 0; idx < 2; idx++ {
		start := idx * chunkSize
		end := start + chunkSize
		chunkBytes := fileData[start:end]

		chunkHash := sha256.Sum256(chunkBytes)
		chunkHashHex := hex.EncodeToString(chunkHash[:])

		chunkReq := httptest.NewRequest("PUT", fmt.Sprintf("/api/transfers/%s/chunks/%d", transferID, idx), bytes.NewReader(chunkBytes))
		chunkReq.Header.Set("Authorization", "Bearer "+token)
		chunkReq.Header.Set("X-Chunk-SHA256", chunkHashHex)
		chunkW := httptest.NewRecorder()
		router1.ServeHTTP(chunkW, chunkReq)

		if chunkW.Code != http.StatusOK {
			t.Fatalf("failed to upload chunk %d: %d %s", idx, chunkW.Code, chunkW.Body.String())
		}
	}

	// 3. Simulate Server Crash & Restart: Close DB, re-open from same file
	db1.Close()

	db2, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("failed to reopen db on restart: %v", err)
	}
	defer db2.Close()

	// Run startup reconciliation
	reconciled, removed, err := db2.ReconcileStartupState()
	if err != nil {
		t.Fatalf("startup reconciliation error: %v", err)
	}
	if reconciled != 2 || removed != 0 {
		t.Fatalf("expected 2 reconciled chunks after restart, got reconciled=%d removed=%d", reconciled, removed)
	}

	mgr2 := transfer.NewManager(db2, chunkDir, completedDir)
	router2 := api.NewRouter(mgr2, webDir)

	// 4. Client queries /missing after restart
	missingReq := httptest.NewRequest("GET", fmt.Sprintf("/api/transfers/%s/missing", transferID), nil)
	missingReq.Header.Set("Authorization", "Bearer "+token)
	missingW := httptest.NewRecorder()
	router2.ServeHTTP(missingW, missingReq)

	var missingData map[string]any
	json.NewDecoder(missingW.Body).Decode(&missingData)
	missingList := missingData["missing_chunks"].([]any)
	if len(missingList) != 3 {
		t.Fatalf("expected 3 missing chunks [2, 3, 4], got %v", missingList)
	}

	// 5. Client resumes: sends ONLY missing chunks 2, 3, 4
	for _, m := range missingList {
		idx := int(m.(float64))
		start := idx * chunkSize
		end := start + chunkSize
		chunkBytes := fileData[start:end]

		chunkHash := sha256.Sum256(chunkBytes)
		chunkHashHex := hex.EncodeToString(chunkHash[:])

		chunkReq := httptest.NewRequest("PUT", fmt.Sprintf("/api/transfers/%s/chunks/%d", transferID, idx), bytes.NewReader(chunkBytes))
		chunkReq.Header.Set("Authorization", "Bearer "+token)
		chunkReq.Header.Set("X-Chunk-SHA256", chunkHashHex)
		chunkW := httptest.NewRecorder()
		router2.ServeHTTP(chunkW, chunkReq)

		if chunkW.Code != http.StatusOK {
			t.Fatalf("failed to upload resume chunk %d: %d", idx, chunkW.Code)
		}
	}

	// 6. Complete transfer & verify
	completeReq := httptest.NewRequest("POST", fmt.Sprintf("/api/transfers/%s/complete", transferID), nil)
	completeReq.Header.Set("Authorization", "Bearer "+token)
	completeW := httptest.NewRecorder()
	router2.ServeHTTP(completeW, completeReq)

	if completeW.Code != http.StatusOK {
		t.Fatalf("failed to complete transfer: %d %s", completeW.Code, completeW.Body.String())
	}

	// 7. Download file and verify bit-for-bit
	downloadReq := httptest.NewRequest("GET", fmt.Sprintf("/api/transfers/%s/download", transferID), nil)
	downloadW := httptest.NewRecorder()
	router2.ServeHTTP(downloadW, downloadReq)

	if downloadW.Code != http.StatusOK {
		t.Fatalf("download failed with code %d", downloadW.Code)
	}

	downloadedBytes, err := io.ReadAll(downloadW.Body)
	if err != nil {
		t.Fatalf("failed to read downloaded body: %v", err)
	}

	if !bytes.Equal(downloadedBytes, fileData) {
		t.Fatalf("E2E verification failed: downloaded bytes do not match original file!")
	}

	t.Logf("SUCCESS: 5-chunk file successfully transferred, interrupted, recovered across server restart, verified, and downloaded bit-for-bit!")
}

// TestDownloadIncompleteFileRejected verifies that attempting to download a file before
// completion returns an error and never exposes partially uploaded chunks (PDF Section 12).
func TestDownloadIncompleteFileRejected(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "incomplete.db")
	chunkDir := filepath.Join(tempDir, "chunks")
	completedDir := filepath.Join(tempDir, "completed")
	os.MkdirAll(chunkDir, 0755)
	os.MkdirAll(completedDir, 0755)

	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	mgr := transfer.NewManager(db, chunkDir, completedDir)
	router := api.NewRouter(mgr, "")

	// 1. Create a transfer
	createPayload, _ := json.Marshal(map[string]any{
		"original_name":   "incomplete_doc.pdf",
		"total_size":      1024,
		"chunk_size":      512,
		"expected_sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	req := httptest.NewRequest("POST", "/api/transfers", bytes.NewReader(createPayload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var createResp map[string]any
	json.NewDecoder(w.Body).Decode(&createResp)
	transferID := createResp["id"].(string)

	// 2. Attempt download immediately — must be rejected (400 Bad Request)
	dlReq := httptest.NewRequest("GET", fmt.Sprintf("/api/transfers/%s/download", transferID), nil)
	dlW := httptest.NewRecorder()
	router.ServeHTTP(dlW, dlReq)

	if dlW.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when downloading incomplete transfer, got %d", dlW.Code)
	}
}

// TestConcurrentTransfers runs multiple simultaneous uploads in parallel goroutines to ensure
// shared state, SQLite WAL concurrency, and chunk storage remain uncorrupted (PDF Section 12).
func TestConcurrentTransfers(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "concurrent.db")
	chunkDir := filepath.Join(tempDir, "chunks")
	completedDir := filepath.Join(tempDir, "completed")
	os.MkdirAll(chunkDir, 0755)
	os.MkdirAll(completedDir, 0755)

	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	mgr := transfer.NewManager(db, chunkDir, completedDir)
	router := api.NewRouter(mgr, "")

	numTransfers := 4
	errChan := make(chan error, numTransfers)

	for i := 0; i < numTransfers; i++ {
		go func(workerID int) {
			// Generate unique payload for this worker
			fileData := make([]byte, 1024)
			for j := range fileData {
				fileData[j] = byte((workerID*37 + j) % 256)
			}
			hash := sha256.Sum256(fileData)
			expectedHash := hex.EncodeToString(hash[:])

			createPayload, _ := json.Marshal(map[string]any{
				"original_name":   fmt.Sprintf("worker_%d_file.bin", workerID),
				"total_size":      len(fileData),
				"chunk_size":      512,
				"expected_sha256": expectedHash,
			})

			// 1. Create transfer
			req := httptest.NewRequest("POST", "/api/transfers", bytes.NewReader(createPayload))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusCreated {
				errChan <- fmt.Errorf("worker %d: create failed with code %d", workerID, w.Code)
				return
			}

			var createResp map[string]any
			json.NewDecoder(w.Body).Decode(&createResp)
			tID := createResp["id"].(string)
			token := createResp["token"].(string)

			// 2. Upload chunks (2 chunks of 512 bytes)
			for idx := 0; idx < 2; idx++ {
				chunkBytes := fileData[idx*512 : (idx+1)*512]
				chunkReq := httptest.NewRequest("PUT", fmt.Sprintf("/api/transfers/%s/chunks/%d", tID, idx), bytes.NewReader(chunkBytes))
				chunkReq.Header.Set("Authorization", "Bearer "+token)
				chunkW := httptest.NewRecorder()
				router.ServeHTTP(chunkW, chunkReq)
				if chunkW.Code != http.StatusOK {
					errChan <- fmt.Errorf("worker %d: chunk %d failed with code %d", workerID, idx, chunkW.Code)
					return
				}
			}

			// 3. Complete transfer
			completeReq := httptest.NewRequest("POST", fmt.Sprintf("/api/transfers/%s/complete", tID), nil)
			completeReq.Header.Set("Authorization", "Bearer "+token)
			completeW := httptest.NewRecorder()
			router.ServeHTTP(completeW, completeReq)
			if completeW.Code != http.StatusOK {
				errChan <- fmt.Errorf("worker %d: complete failed with code %d: %s", workerID, completeW.Code, completeW.Body.String())
				return
			}

			// 4. Download and verify
			dlReq := httptest.NewRequest("GET", fmt.Sprintf("/api/transfers/%s/download", tID), nil)
			dlW := httptest.NewRecorder()
			router.ServeHTTP(dlW, dlReq)
			if dlW.Code != http.StatusOK {
				errChan <- fmt.Errorf("worker %d: download failed with code %d", workerID, dlW.Code)
				return
			}

			if !bytes.Equal(dlW.Body.Bytes(), fileData) {
				errChan <- fmt.Errorf("worker %d: downloaded content mismatch", workerID)
				return
			}

			errChan <- nil
		}(i)
	}

	for i := 0; i < numTransfers; i++ {
		if err := <-errChan; err != nil {
			t.Fatalf("Concurrent transfer failure: %v", err)
		}
	}
}

