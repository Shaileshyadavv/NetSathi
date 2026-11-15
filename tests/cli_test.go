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

// TestCLIUploadAndDownloadFlow simulates the exact end-to-end workflow executed
// by the NetSathi CLI: hashing, session creation, chunk-by-chunk streaming,
// progress verification, and bit-exact download, completely in-process.
func TestCLIUploadAndDownloadFlow(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "cli_test.db")
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

	// 1. Prepare sample source file
	sourceContent := bytes.Repeat([]byte("NetSathi CLI Test Payload! "), 100) // ~2.7 KB
	sourceHash := sha256.Sum256(sourceContent)
	sourceHashHex := hex.EncodeToString(sourceHash[:])

	sourcePath := filepath.Join(tempDir, "sample_cli.txt")
	if err := os.WriteFile(sourcePath, sourceContent, 0644); err != nil {
		t.Fatalf("failed to create source file: %v", err)
	}

	chunkSize := int64(1024)

	// 2. Create transfer via API (same as CLI upload)
	createReqBody, _ := json.Marshal(map[string]any{
		"original_name":   "sample_cli.txt",
		"total_size":      len(sourceContent),
		"chunk_size":      chunkSize,
		"expected_sha256": sourceHashHex,
	})

	createReq := httptest.NewRequest("POST", "/api/transfers", bytes.NewReader(createReqBody))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d", createRec.Code)
	}

	var session struct {
		ID          string `json:"id"`
		Token       string `json:"token"`
		TotalChunks int    `json:"total_chunks"`
		ChunkSize   int64  `json:"chunk_size"`
	}
	json.NewDecoder(createRec.Body).Decode(&session)

	if session.TotalChunks != 3 {
		t.Fatalf("expected 3 chunks, got %d", session.TotalChunks)
	}

	// 3. Upload chunks
	file, err := os.Open(sourcePath)
	if err != nil {
		t.Fatalf("failed to open source file: %v", err)
	}
	defer file.Close()

	for idx := 0; idx < session.TotalChunks; idx++ {
		start := int64(idx) * session.ChunkSize
		bufSize := session.ChunkSize
		if start+bufSize > int64(len(sourceContent)) {
			bufSize = int64(len(sourceContent)) - start
		}
		chunkBytes := make([]byte, bufSize)
		file.ReadAt(chunkBytes, start)

		cHash := sha256.Sum256(chunkBytes)
		cHashHex := hex.EncodeToString(cHash[:])

		putReq := httptest.NewRequest("PUT", fmt.Sprintf("/api/transfers/%s/chunks/%d", session.ID, idx), bytes.NewReader(chunkBytes))
		putReq.Header.Set("Authorization", "Bearer "+session.Token)
		putReq.Header.Set("X-Chunk-SHA256", cHashHex)
		putRec := httptest.NewRecorder()
		router.ServeHTTP(putRec, putReq)

		if putRec.Code != http.StatusOK {
			t.Fatalf("chunk %d upload returned status %d", idx, putRec.Code)
		}
	}

	// 4. Complete and verify
	compReq := httptest.NewRequest("POST", fmt.Sprintf("/api/transfers/%s/complete", session.ID), nil)
	compReq.Header.Set("Authorization", "Bearer "+session.Token)
	compRec := httptest.NewRecorder()
	router.ServeHTTP(compRec, compReq)

	if compRec.Code != http.StatusOK {
		t.Fatalf("complete returned status %d", compRec.Code)
	}

	// 5. Query status
	statusReq := httptest.NewRequest("GET", fmt.Sprintf("/api/transfers/%s", session.ID), nil)
	statusRec := httptest.NewRecorder()
	router.ServeHTTP(statusRec, statusReq)

	var statusData map[string]any
	json.NewDecoder(statusRec.Body).Decode(&statusData)
	if statusData["percentage"].(float64) != 100 {
		t.Errorf("expected 100%% progress, got %v", statusData["percentage"])
	}

	// 6. Download and verify bit-for-bit
	dlReq := httptest.NewRequest("GET", fmt.Sprintf("/api/transfers/%s/download", session.ID), nil)
	dlRec := httptest.NewRecorder()
	router.ServeHTTP(dlRec, dlReq)

	if dlRec.Code != http.StatusOK {
		t.Fatalf("download returned status %d", dlRec.Code)
	}

	downloaded, err := io.ReadAll(dlRec.Body)
	if err != nil {
		t.Fatalf("failed to read downloaded content: %v", err)
	}

	if !bytes.Equal(downloaded, sourceContent) {
		t.Fatalf("downloaded content does not match original file!")
	}
}
