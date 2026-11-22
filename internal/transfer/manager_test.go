package transfer_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"netsathi/internal/storage"
	"netsathi/internal/transfer"
)

// newTestManager creates a Manager backed by a temp SQLite DB and temp dirs.
func newTestManager(t *testing.T) *transfer.Manager {
	t.Helper()
	dir := t.TempDir()
	chunkDir := filepath.Join(dir, "chunks")
	completedDir := filepath.Join(dir, "completed")
	os.MkdirAll(chunkDir, 0755)
	os.MkdirAll(completedDir, 0755)

	db, err := storage.NewDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	return transfer.NewManager(db, chunkDir, completedDir)
}

// makeTestData generates deterministic test bytes and their SHA-256 hex.
func makeTestData(size int) ([]byte, string) {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte((i * 7) % 251)
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:])
}

// createTransferHelper creates a valid transfer and returns the response.
func createTransferHelper(t *testing.T, mgr *transfer.Manager, name string, data []byte, chunkSize int64) *transfer.CreateTransferResponse {
	t.Helper()
	sum := sha256.Sum256(data)
	resp, err := mgr.CreateTransfer(&transfer.CreateTransferRequest{
		OriginalName:   name,
		TotalSize:      int64(len(data)),
		ChunkSize:      chunkSize,
		ExpectedSHA256: hex.EncodeToString(sum[:]),
	})
	if err != nil {
		t.Fatalf("CreateTransfer failed: %v", err)
	}
	return resp
}

// ============================================================
// CreateTransfer Tests
// ============================================================

func TestCreateTransfer_Valid(t *testing.T) {
	mgr := newTestManager(t)
	data, hash := makeTestData(2048)
	_ = data

	resp, err := mgr.CreateTransfer(&transfer.CreateTransferRequest{
		OriginalName:   "test.bin",
		TotalSize:      2048,
		ChunkSize:      1024,
		ExpectedSHA256: hash,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID == "" {
		t.Error("expected non-empty ID")
	}
	if resp.Token == "" {
		t.Error("expected non-empty Token")
	}
	if resp.TotalChunks != 2 {
		t.Errorf("expected 2 chunks, got %d", resp.TotalChunks)
	}
	if resp.ChunkSize != 1024 {
		t.Errorf("expected chunk size 1024, got %d", resp.ChunkSize)
	}
}

func TestCreateTransfer_ValidationErrors(t *testing.T) {
	mgr := newTestManager(t)
	_, validHash := makeTestData(100)

	tests := []struct {
		name    string
		req     transfer.CreateTransferRequest
		wantErr string
	}{
		{
			name:    "missing original_name",
			req:     transfer.CreateTransferRequest{TotalSize: 100, ChunkSize: 50, ExpectedSHA256: validHash},
			wantErr: "original_name is required",
		},
		{
			name:    "zero total_size",
			req:     transfer.CreateTransferRequest{OriginalName: "f.bin", TotalSize: 0, ChunkSize: 50, ExpectedSHA256: validHash},
			wantErr: "total_size must be positive",
		},
		{
			name:    "negative chunk_size",
			req:     transfer.CreateTransferRequest{OriginalName: "f.bin", TotalSize: 100, ChunkSize: -1, ExpectedSHA256: validHash},
			wantErr: "chunk_size must be positive",
		},
		{
			name:    "chunk_size exceeds 10MB",
			req:     transfer.CreateTransferRequest{OriginalName: "f.bin", TotalSize: 100, ChunkSize: 11 * 1024 * 1024, ExpectedSHA256: validHash},
			wantErr: "chunk_size cannot exceed 10 MB",
		},
		{
			name:    "missing expected_sha256",
			req:     transfer.CreateTransferRequest{OriginalName: "f.bin", TotalSize: 100, ChunkSize: 50},
			wantErr: "expected_sha256 is required",
		},
		{
			name:    "invalid sha256 length",
			req:     transfer.CreateTransferRequest{OriginalName: "f.bin", TotalSize: 100, ChunkSize: 50, ExpectedSHA256: "tooshort"},
			wantErr: "expected_sha256 must be a 64-character hex string",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mgr.CreateTransfer(&tc.req)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestCreateTransfer_SanitizesFilename(t *testing.T) {
	mgr := newTestManager(t)
	_, hash := makeTestData(100)

	resp, err := mgr.CreateTransfer(&transfer.CreateTransferRequest{
		OriginalName:   "../../etc/passwd",
		TotalSize:      100,
		ChunkSize:      100,
		ExpectedSHA256: hash,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Sanitized name must not contain path separators
	if strings.Contains(resp.ID, "..") {
		t.Error("transfer ID should not contain path traversal")
	}
	// We can verify via GetTransferProgress that the name is safe
	progress, _ := mgr.GetTransferProgress(resp.ID)
	if strings.Contains(progress.Transfer.OriginalName, "..") || strings.Contains(progress.Transfer.OriginalName, "/") {
		t.Errorf("sanitized name contains path chars: %q", progress.Transfer.OriginalName)
	}
}

// ============================================================
// SaveChunk Tests
// ============================================================

func TestSaveChunk_Valid(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(1024)
	resp := createTransferHelper(t, mgr, "save.bin", data, 1024)

	written, err := mgr.SaveChunk(resp.ID, resp.Token, 0, "", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("SaveChunk failed: %v", err)
	}
	if written != int64(len(data)) {
		t.Errorf("expected %d bytes written, got %d", len(data), written)
	}
}

func TestSaveChunk_WithValidChecksum(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(512)
	resp := createTransferHelper(t, mgr, "checksum.bin", data, 512)

	sum := sha256.Sum256(data)
	checksum := hex.EncodeToString(sum[:])

	_, err := mgr.SaveChunk(resp.ID, resp.Token, 0, checksum, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("SaveChunk with valid checksum failed: %v", err)
	}
}

func TestSaveChunk_WithInvalidChecksum(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(512)
	resp := createTransferHelper(t, mgr, "badcheck.bin", data, 512)

	badChecksum := strings.Repeat("0", 64)
	_, err := mgr.SaveChunk(resp.ID, resp.Token, 0, badChecksum, bytes.NewReader(data))
	if err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("expected checksum mismatch error, got: %v", err)
	}
}

func TestSaveChunk_WrongToken(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(512)
	resp := createTransferHelper(t, mgr, "token.bin", data, 512)

	_, err := mgr.SaveChunk(resp.ID, "wrong-token-value", 0, "", bytes.NewReader(data))
	if err == nil {
		t.Fatal("expected unauthorized error, got nil")
	}
}

func TestSaveChunk_OutOfRangeIndex(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(512)
	resp := createTransferHelper(t, mgr, "range.bin", data, 512)

	// Only 1 chunk (index 0 is valid), so index 5 is out of range
	_, err := mgr.SaveChunk(resp.ID, resp.Token, 5, "", bytes.NewReader(data))
	if err == nil {
		t.Fatal("expected out-of-range error, got nil")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("expected out of range error, got: %v", err)
	}
}

func TestSaveChunk_Idempotent(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(512)
	resp := createTransferHelper(t, mgr, "idem.bin", data, 512)

	for i := 0; i < 3; i++ {
		_, err := mgr.SaveChunk(resp.ID, resp.Token, 0, "", bytes.NewReader(data))
		if err != nil {
			t.Fatalf("SaveChunk attempt %d failed: %v", i+1, err)
		}
	}

	// Should still be completable
	_, err := mgr.CompleteTransfer(resp.ID, resp.Token)
	if err != nil {
		t.Fatalf("CompleteTransfer after idempotent upload failed: %v", err)
	}
}

func TestSaveChunk_TransferNotFound(t *testing.T) {
	mgr := newTestManager(t)
	_, err := mgr.SaveChunk("nonexistent-id", "token", 0, "", bytes.NewReader([]byte("data")))
	if err == nil {
		t.Fatal("expected error for nonexistent transfer, got nil")
	}
}

// ============================================================
// GetMissingChunks Tests
// ============================================================

func TestGetMissingChunks_AllMissing(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(3 * 512)
	resp := createTransferHelper(t, mgr, "missing.bin", data, 512)

	missing, err := mgr.GetMissingChunks(resp.ID, resp.Token)
	if err != nil {
		t.Fatalf("GetMissingChunks failed: %v", err)
	}
	if len(missing) != 3 {
		t.Errorf("expected 3 missing chunks, got %d: %v", len(missing), missing)
	}
}

func TestGetMissingChunks_PartiallyReceived(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(3 * 512)
	resp := createTransferHelper(t, mgr, "partial.bin", data, 512)

	// Upload chunks 0 and 2 (skip 1)
	mgr.SaveChunk(resp.ID, resp.Token, 0, "", bytes.NewReader(data[0:512]))
	mgr.SaveChunk(resp.ID, resp.Token, 2, "", bytes.NewReader(data[1024:1536]))

	missing, err := mgr.GetMissingChunks(resp.ID, resp.Token)
	if err != nil {
		t.Fatalf("GetMissingChunks failed: %v", err)
	}
	if len(missing) != 1 || missing[0] != 1 {
		t.Errorf("expected missing=[1], got %v", missing)
	}
}

func TestGetMissingChunks_NoneAfterAllUploaded(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(512)
	resp := createTransferHelper(t, mgr, "nonemsng.bin", data, 512)

	mgr.SaveChunk(resp.ID, resp.Token, 0, "", bytes.NewReader(data))

	missing, err := mgr.GetMissingChunks(resp.ID, resp.Token)
	if err != nil {
		t.Fatalf("GetMissingChunks failed: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("expected 0 missing chunks, got %d: %v", len(missing), missing)
	}
}

func TestGetMissingChunks_WrongToken(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(512)
	resp := createTransferHelper(t, mgr, "tok.bin", data, 512)

	_, err := mgr.GetMissingChunks(resp.ID, "bad-token")
	if err == nil {
		t.Fatal("expected unauthorized error, got nil")
	}
}

// ============================================================
// GetTransferProgress Tests
// ============================================================

func TestGetTransferProgress_InitialState(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(2 * 512)
	resp := createTransferHelper(t, mgr, "prog.bin", data, 512)

	progress, err := mgr.GetTransferProgress(resp.ID)
	if err != nil {
		t.Fatalf("GetTransferProgress failed: %v", err)
	}
	if progress.ReceivedChunks != 0 {
		t.Errorf("expected 0 received chunks, got %d", progress.ReceivedChunks)
	}
	if progress.Percentage != 0 {
		t.Errorf("expected 0%%, got %.2f%%", progress.Percentage)
	}
	if progress.Transfer.Token != "" {
		t.Error("token should be stripped from progress response")
	}
}

func TestGetTransferProgress_AfterPartialUpload(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(4 * 512)
	resp := createTransferHelper(t, mgr, "half.bin", data, 512)

	// Upload 2 of 4 chunks
	mgr.SaveChunk(resp.ID, resp.Token, 0, "", bytes.NewReader(data[0:512]))
	mgr.SaveChunk(resp.ID, resp.Token, 1, "", bytes.NewReader(data[512:1024]))

	progress, err := mgr.GetTransferProgress(resp.ID)
	if err != nil {
		t.Fatalf("GetTransferProgress failed: %v", err)
	}
	if progress.ReceivedChunks != 2 {
		t.Errorf("expected 2 received chunks, got %d", progress.ReceivedChunks)
	}
	if progress.Percentage != 50.0 {
		t.Errorf("expected 50%%, got %.2f%%", progress.Percentage)
	}
}

func TestGetTransferProgress_NotFound(t *testing.T) {
	mgr := newTestManager(t)
	_, err := mgr.GetTransferProgress("does-not-exist")
	if err == nil {
		t.Fatal("expected error for missing transfer, got nil")
	}
}

// ============================================================
// CompleteTransfer Tests
// ============================================================

func TestCompleteTransfer_Success(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(3 * 512)
	resp := createTransferHelper(t, mgr, "complete.bin", data, 512)

	for i := 0; i < 3; i++ {
		start, end := i*512, (i+1)*512
		if _, err := mgr.SaveChunk(resp.ID, resp.Token, i, "", bytes.NewReader(data[start:end])); err != nil {
			t.Fatalf("SaveChunk %d failed: %v", i, err)
		}
	}

	progress, err := mgr.CompleteTransfer(resp.ID, resp.Token)
	if err != nil {
		t.Fatalf("CompleteTransfer failed: %v", err)
	}
	if progress.Transfer.Status != storage.StatusCompleted {
		t.Errorf("expected Completed status, got %s", progress.Transfer.Status)
	}
	if progress.Percentage != 100.0 {
		t.Errorf("expected 100%%, got %.2f%%", progress.Percentage)
	}
}

func TestCompleteTransfer_WrongToken(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(512)
	resp := createTransferHelper(t, mgr, "compwrong.bin", data, 512)

	mgr.SaveChunk(resp.ID, resp.Token, 0, "", bytes.NewReader(data))

	_, err := mgr.CompleteTransfer(resp.ID, "wrong-token")
	if err == nil {
		t.Fatal("expected unauthorized error, got nil")
	}
}

func TestCompleteTransfer_MissingChunks(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(2 * 512)
	resp := createTransferHelper(t, mgr, "compinc.bin", data, 512)

	// Upload only chunk 0
	mgr.SaveChunk(resp.ID, resp.Token, 0, "", bytes.NewReader(data[0:512]))

	_, err := mgr.CompleteTransfer(resp.ID, resp.Token)
	if err == nil {
		t.Fatal("expected error for missing chunks, got nil")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("expected missing chunks error, got: %v", err)
	}
}

func TestCompleteTransfer_HashMismatch(t *testing.T) {
	mgr := newTestManager(t)
	realData, _ := makeTestData(512)
	wrongHash := strings.Repeat("a", 64)

	// Create transfer with wrong expected hash
	resp, err := mgr.CreateTransfer(&transfer.CreateTransferRequest{
		OriginalName:   "mismatch.bin",
		TotalSize:      512,
		ChunkSize:      512,
		ExpectedSHA256: wrongHash,
	})
	if err != nil {
		t.Fatalf("CreateTransfer failed: %v", err)
	}

	mgr.SaveChunk(resp.ID, resp.Token, 0, "", bytes.NewReader(realData))

	_, err = mgr.CompleteTransfer(resp.ID, resp.Token)
	if err == nil {
		t.Fatal("expected hash mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "integrity check failed") && !strings.Contains(err.Error(), "SHA-256") {
		t.Errorf("expected hash mismatch error, got: %v", err)
	}
}

// ============================================================
// ListTransfers Tests
// ============================================================

func TestListTransfers_Empty(t *testing.T) {
	mgr := newTestManager(t)
	list, err := mgr.ListTransfers(10)
	if err != nil {
		t.Fatalf("ListTransfers failed: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 transfers, got %d", len(list))
	}
}

func TestListTransfers_ReturnsAll(t *testing.T) {
	mgr := newTestManager(t)
	_, hash := makeTestData(100)

	for i := 0; i < 3; i++ {
		mgr.CreateTransfer(&transfer.CreateTransferRequest{
			OriginalName:   "file.bin",
			TotalSize:      100,
			ChunkSize:      100,
			ExpectedSHA256: hash,
		})
	}

	list, err := mgr.ListTransfers(10)
	if err != nil {
		t.Fatalf("ListTransfers failed: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("expected 3 transfers, got %d", len(list))
	}
	// Tokens must be stripped
	for _, tr := range list {
		if tr.Token != "" {
			t.Error("token should be stripped from ListTransfers response")
		}
	}
}

func TestListTransfers_RespectsLimit(t *testing.T) {
	mgr := newTestManager(t)
	_, hash := makeTestData(100)

	for i := 0; i < 5; i++ {
		mgr.CreateTransfer(&transfer.CreateTransferRequest{
			OriginalName:   "f.bin",
			TotalSize:      100,
			ChunkSize:      100,
			ExpectedSHA256: hash,
		})
	}

	list, err := mgr.ListTransfers(2)
	if err != nil {
		t.Fatalf("ListTransfers failed: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 (limited) transfers, got %d", len(list))
	}
}

// ============================================================
// GetCompletedFilePath Tests
// ============================================================

func TestGetCompletedFilePath_Completed(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(512)
	resp := createTransferHelper(t, mgr, "dl.bin", data, 512)

	mgr.SaveChunk(resp.ID, resp.Token, 0, "", bytes.NewReader(data))
	mgr.CompleteTransfer(resp.ID, resp.Token)

	path, name, err := mgr.GetCompletedFilePath(resp.ID)
	if err != nil {
		t.Fatalf("GetCompletedFilePath failed: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty path")
	}
	if name == "" {
		t.Error("expected non-empty original name")
	}
	// File must exist on disk
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("file does not exist on disk: %s", path)
	}
}

func TestGetCompletedFilePath_NotYetCompleted(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(512)
	resp := createTransferHelper(t, mgr, "notdone.bin", data, 512)

	_, _, err := mgr.GetCompletedFilePath(resp.ID)
	if err == nil {
		t.Fatal("expected error for incomplete transfer, got nil")
	}
}

// ============================================================
// VerifyToken Tests
// ============================================================

func TestVerifyToken_Valid(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(512)
	resp := createTransferHelper(t, mgr, "vtok.bin", data, 512)

	if err := mgr.VerifyToken(resp.ID, resp.Token); err != nil {
		t.Fatalf("VerifyToken with correct token failed: %v", err)
	}
}

func TestVerifyToken_WrongToken(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(512)
	resp := createTransferHelper(t, mgr, "vtokbad.bin", data, 512)

	if err := mgr.VerifyToken(resp.ID, "wrong"); err == nil {
		t.Fatal("expected error for wrong token, got nil")
	}
}

func TestVerifyToken_TransferNotFound(t *testing.T) {
	mgr := newTestManager(t)
	if err := mgr.VerifyToken("nonexistent", "token"); err == nil {
		t.Fatal("expected error for nonexistent transfer, got nil")
	}
}

// ============================================================
// CleanupExpired Tests
// ============================================================

func TestCleanupExpired_RemovesOldTransfers(t *testing.T) {
	mgr := newTestManager(t)
	_, hash := makeTestData(100)

	// Create a transfer
	mgr.CreateTransfer(&transfer.CreateTransferRequest{
		OriginalName:   "expire.bin",
		TotalSize:      100,
		ChunkSize:      100,
		ExpectedSHA256: hash,
	})

	// Use a negative maxAge so cutoff = now + |maxAge|, catching all transfers
	// (time.Now().Add(-(-1*hour)) = time.Now().Add(1*hour))
	n, err := mgr.CleanupExpired(-1 * time.Hour)
	if err != nil {
		t.Fatalf("CleanupExpired failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 transfer cleaned, got %d", n)
	}

	// Transfer list should now be empty
	list, _ := mgr.ListTransfers(10)
	if len(list) != 0 {
		t.Errorf("expected 0 transfers after cleanup, got %d", len(list))
	}
}

func TestCleanupExpired_PreservesCompletedTransfers(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(512)
	resp := createTransferHelper(t, mgr, "keep.bin", data, 512)

	// Complete the transfer
	mgr.SaveChunk(resp.ID, resp.Token, 0, "", bytes.NewReader(data))
	mgr.CompleteTransfer(resp.ID, resp.Token)

	// Cleanup should NOT remove completed transfers
	n, err := mgr.CleanupExpired(-1 * time.Hour)
	if err != nil {
		t.Fatalf("CleanupExpired failed: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 completed transfers cleaned, got %d", n)
	}
}

func TestDeleteTransfer(t *testing.T) {
	mgr := newTestManager(t)
	data, _ := makeTestData(1024)
	resp := createTransferHelper(t, mgr, "to_delete.bin", data, 1024)

	// Save chunk and complete
	mgr.SaveChunk(resp.ID, resp.Token, 0, "", bytes.NewReader(data))
	mgr.CompleteTransfer(resp.ID, resp.Token)

	// Verify file exists
	path, _, err := mgr.GetCompletedFilePath(resp.ID)
	if err != nil {
		t.Fatalf("GetCompletedFilePath failed: %v", err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("expected completed file to exist")
	}

	// Delete transfer
	if err := mgr.DeleteTransfer(resp.ID); err != nil {
		t.Fatalf("DeleteTransfer failed: %v", err)
	}

	// Verify file is removed from disk
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected completed file to be deleted from disk")
	}

	// Verify cannot get transfer
	_, err = mgr.GetTransferProgress(resp.ID)
	if err == nil {
		t.Errorf("expected error getting deleted transfer")
	}
}
