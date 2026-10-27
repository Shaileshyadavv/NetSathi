package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"netsathi/internal/storage"
	"netsathi/internal/transfer"
)

// setupTestRouter creates a fully wired router with temp directories (no TCP needed).
func setupTestRouter(t *testing.T) http.Handler {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	chunkDir := filepath.Join(tempDir, "chunks")
	completedDir := filepath.Join(tempDir, "completed")
	webDir := filepath.Join(tempDir, "web")

	os.MkdirAll(chunkDir, 0755)
	os.MkdirAll(completedDir, 0755)
	os.MkdirAll(webDir, 0755)
	os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<html><body>test</body></html>"), 0644)

	db, err := storage.NewDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mgr := transfer.NewManager(db, chunkDir, completedDir)
	return NewRouter(mgr, webDir)
}

// doRequest is a helper that performs an in-process HTTP request via httptest.NewRecorder.
func doRequest(router http.Handler, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

func TestHealthEndpoint(t *testing.T) {
	router := setupTestRouter(t)
	rr := doRequest(router, "GET", "/healthz", nil, nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var body map[string]string
	json.NewDecoder(rr.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("expected status ok, got %s", body["status"])
	}
}

func TestFullTransferLifecycle(t *testing.T) {
	router := setupTestRouter(t)

	// --- Step 1: Prepare a test file (split into 2 chunks) ---
	chunk1 := bytes.Repeat([]byte("A"), 1024) // 1 KB
	chunk2 := bytes.Repeat([]byte("B"), 512)  // 512 bytes
	fullFile := append(chunk1, chunk2...)

	hasher := sha256.New()
	hasher.Write(fullFile)
	expectedHash := hex.EncodeToString(hasher.Sum(nil))

	// --- Step 2: Create transfer ---
	createBody, _ := json.Marshal(map[string]any{
		"original_name":   "test_file.bin",
		"total_size":      len(fullFile),
		"chunk_size":      1024,
		"expected_sha256": expectedHash,
	})

	rr := doRequest(router, "POST", "/api/transfers", bytes.NewReader(createBody),
		map[string]string{"Content-Type": "application/json"})

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var createResp map[string]any
	json.NewDecoder(rr.Body).Decode(&createResp)
	transferID := createResp["id"].(string)
	token := createResp["token"].(string)

	if transferID == "" || token == "" {
		t.Fatalf("expected non-empty id and token")
	}
	totalChunks := int(createResp["total_chunks"].(float64))
	if totalChunks != 2 {
		t.Fatalf("expected 2 chunks, got %d", totalChunks)
	}

	authHeader := map[string]string{"Authorization": "Bearer " + token}

	// --- Step 3: Check missing chunks (should be [0, 1]) ---
	rr = doRequest(router, "GET", "/api/transfers/"+transferID+"/missing", nil, authHeader)

	var missingBody map[string]any
	json.NewDecoder(rr.Body).Decode(&missingBody)
	missingCount := int(missingBody["missing_count"].(float64))
	if missingCount != 2 {
		t.Fatalf("expected 2 missing chunks, got %d", missingCount)
	}

	// --- Step 4: Upload chunk 0 ---
	rr = doRequest(router, "PUT", "/api/transfers/"+transferID+"/chunks/0",
		bytes.NewReader(chunk1), authHeader)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for chunk 0 upload, got %d: %s", rr.Code, rr.Body.String())
	}

	// --- Step 5: Upload chunk 1 ---
	rr = doRequest(router, "PUT", "/api/transfers/"+transferID+"/chunks/1",
		bytes.NewReader(chunk2), authHeader)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for chunk 1 upload, got %d: %s", rr.Code, rr.Body.String())
	}

	// --- Step 6: Verify missing chunks is now empty ---
	rr = doRequest(router, "GET", "/api/transfers/"+transferID+"/missing", nil, authHeader)

	var missingBody2 map[string]any
	json.NewDecoder(rr.Body).Decode(&missingBody2)
	if int(missingBody2["missing_count"].(float64)) != 0 {
		t.Fatalf("expected 0 missing chunks after upload")
	}

	// --- Step 7: Complete transfer ---
	rr = doRequest(router, "POST", "/api/transfers/"+transferID+"/complete", nil, authHeader)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for complete, got %d: %s", rr.Code, rr.Body.String())
	}

	// --- Step 8: Verify transfer status ---
	rr = doRequest(router, "GET", "/api/transfers/"+transferID, nil, nil)

	var progress map[string]any
	json.NewDecoder(rr.Body).Decode(&progress)
	transferData := progress["transfer"].(map[string]any)
	if transferData["status"] != "Completed" {
		t.Errorf("expected Completed status, got %s", transferData["status"])
	}
	if progress["percentage"].(float64) != 100 {
		t.Errorf("expected 100%% progress, got %v", progress["percentage"])
	}

	// --- Step 9: Download the file ---
	rr = doRequest(router, "GET", "/api/transfers/"+transferID+"/download", nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for download, got %d", rr.Code)
	}
	downloaded := rr.Body.Bytes()
	if !bytes.Equal(downloaded, fullFile) {
		t.Errorf("downloaded file content does not match original (got %d bytes, expected %d)", len(downloaded), len(fullFile))
	}
}

func TestTransferListEndpoint(t *testing.T) {
	router := setupTestRouter(t)

	rr := doRequest(router, "GET", "/api/transfers", nil, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var body map[string]any
	json.NewDecoder(rr.Body).Decode(&body)
	if int(body["count"].(float64)) != 0 {
		t.Errorf("expected 0 transfers, got %v", body["count"])
	}
}

func TestChunkUploadWithoutToken(t *testing.T) {
	router := setupTestRouter(t)

	// Create a transfer first
	createBody, _ := json.Marshal(map[string]any{
		"original_name":   "no_auth.bin",
		"total_size":      100,
		"chunk_size":      100,
		"expected_sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	})
	rr := doRequest(router, "POST", "/api/transfers", bytes.NewReader(createBody),
		map[string]string{"Content-Type": "application/json"})

	var createResp map[string]any
	json.NewDecoder(rr.Body).Decode(&createResp)
	transferID := createResp["id"].(string)

	// Try uploading without a token
	rr = doRequest(router, "PUT", "/api/transfers/"+transferID+"/chunks/0",
		bytes.NewReader([]byte("data")), nil)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", rr.Code)
	}
}

func TestChunkUploadWithWrongToken(t *testing.T) {
	router := setupTestRouter(t)

	createBody, _ := json.Marshal(map[string]any{
		"original_name":   "wrong_token.bin",
		"total_size":      100,
		"chunk_size":      100,
		"expected_sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	})
	rr := doRequest(router, "POST", "/api/transfers", bytes.NewReader(createBody),
		map[string]string{"Content-Type": "application/json"})

	var createResp map[string]any
	json.NewDecoder(rr.Body).Decode(&createResp)
	transferID := createResp["id"].(string)

	// Upload with a wrong token
	rr = doRequest(router, "PUT", "/api/transfers/"+transferID+"/chunks/0",
		bytes.NewReader([]byte("data")), map[string]string{"Authorization": "Bearer wrongtoken123"})

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 with wrong token, got %d", rr.Code)
	}
}

func TestIdempotentChunkUpload(t *testing.T) {
	router := setupTestRouter(t)

	chunk := bytes.Repeat([]byte("X"), 512)
	fullFile := chunk
	hasher := sha256.New()
	hasher.Write(fullFile)
	expectedHash := hex.EncodeToString(hasher.Sum(nil))

	createBody, _ := json.Marshal(map[string]any{
		"original_name":   "idempotent.bin",
		"total_size":      512,
		"chunk_size":      512,
		"expected_sha256": expectedHash,
	})
	rr := doRequest(router, "POST", "/api/transfers", bytes.NewReader(createBody),
		map[string]string{"Content-Type": "application/json"})

	var createResp map[string]any
	json.NewDecoder(rr.Body).Decode(&createResp)
	transferID := createResp["id"].(string)
	token := createResp["token"].(string)
	authHeader := map[string]string{"Authorization": "Bearer " + token}

	// Upload the same chunk twice — should be idempotent
	for i := 0; i < 2; i++ {
		rr = doRequest(router, "PUT", "/api/transfers/"+transferID+"/chunks/0",
			bytes.NewReader(chunk), authHeader)
		if rr.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected 200, got %d", i+1, rr.Code)
		}
	}

	// Complete should still succeed
	rr = doRequest(router, "POST", "/api/transfers/"+transferID+"/complete", nil, authHeader)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for complete, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHashMismatchFails(t *testing.T) {
	router := setupTestRouter(t)

	chunk := []byte("real data")
	wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"

	createBody, _ := json.Marshal(map[string]any{
		"original_name":   "mismatch.bin",
		"total_size":      len(chunk),
		"chunk_size":      len(chunk),
		"expected_sha256": wrongHash,
	})
	rr := doRequest(router, "POST", "/api/transfers", bytes.NewReader(createBody),
		map[string]string{"Content-Type": "application/json"})

	var createResp map[string]any
	json.NewDecoder(rr.Body).Decode(&createResp)
	transferID := createResp["id"].(string)
	token := createResp["token"].(string)
	authHeader := map[string]string{"Authorization": "Bearer " + token}

	// Upload chunk
	rr = doRequest(router, "PUT", "/api/transfers/"+transferID+"/chunks/0",
		bytes.NewReader(chunk), authHeader)

	// Complete should fail due to hash mismatch
	rr = doRequest(router, "POST", "/api/transfers/"+transferID+"/complete", nil, authHeader)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for hash mismatch, got %d", rr.Code)
	}

	// Status should be Failed
	rr = doRequest(router, "GET", "/api/transfers/"+transferID, nil, nil)
	var progress map[string]any
	json.NewDecoder(rr.Body).Decode(&progress)
	transferData := progress["transfer"].(map[string]any)
	if transferData["status"] != "Failed" {
		t.Errorf("expected Failed status, got %s", transferData["status"])
	}
}

func TestCompleteWithMissingChunks(t *testing.T) {
	router := setupTestRouter(t)

	chunk := bytes.Repeat([]byte("A"), 100)
	hasher := sha256.New()
	hasher.Write(append(chunk, chunk...))
	expectedHash := hex.EncodeToString(hasher.Sum(nil))

	createBody, _ := json.Marshal(map[string]any{
		"original_name":   "incomplete.bin",
		"total_size":      200,
		"chunk_size":      100,
		"expected_sha256": expectedHash,
	})
	rr := doRequest(router, "POST", "/api/transfers", bytes.NewReader(createBody),
		map[string]string{"Content-Type": "application/json"})

	var createResp map[string]any
	json.NewDecoder(rr.Body).Decode(&createResp)
	transferID := createResp["id"].(string)
	token := createResp["token"].(string)
	authHeader := map[string]string{"Authorization": "Bearer " + token}

	// Upload only chunk 0 (chunk 1 is missing)
	doRequest(router, "PUT", "/api/transfers/"+transferID+"/chunks/0",
		bytes.NewReader(chunk), authHeader)

	// Complete should fail — chunks still missing
	rr = doRequest(router, "POST", "/api/transfers/"+transferID+"/complete", nil, authHeader)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing chunks, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestChunkLevelChecksumValidation(t *testing.T) {
	router := setupTestRouter(t)

	chunkData := []byte("chunk with checksum")
	hasher := sha256.New()
	hasher.Write(chunkData)
	validChunkHash := hex.EncodeToString(hasher.Sum(nil))

	createBody, _ := json.Marshal(map[string]any{
		"original_name":   "chunk_checksum.bin",
		"total_size":      len(chunkData),
		"chunk_size":      len(chunkData),
		"expected_sha256": validChunkHash,
	})
	rr := doRequest(router, "POST", "/api/transfers", bytes.NewReader(createBody),
		map[string]string{"Content-Type": "application/json"})

	var createResp map[string]any
	json.NewDecoder(rr.Body).Decode(&createResp)
	transferID := createResp["id"].(string)
	token := createResp["token"].(string)

	// 1. Upload with INVALID chunk checksum -> should fail with 400
	badHeaders := map[string]string{
		"Authorization":  "Bearer " + token,
		"X-Chunk-SHA256": "1111222233334444555566667777888899990000aaaabbbbccccddddeeeeffff",
	}
	rr = doRequest(router, "PUT", "/api/transfers/"+transferID+"/chunks/0",
		bytes.NewReader(chunkData), badHeaders)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid chunk checksum, got %d", rr.Code)
	}

	// 2. Upload with VALID chunk checksum -> should succeed with 200
	goodHeaders := map[string]string{
		"Authorization":  "Bearer " + token,
		"X-Chunk-SHA256": validChunkHash,
	}
	rr = doRequest(router, "PUT", "/api/transfers/"+transferID+"/chunks/0",
		bytes.NewReader(chunkData), goodHeaders)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for valid chunk checksum, got %d: %s", rr.Code, rr.Body.String())
	}
}

