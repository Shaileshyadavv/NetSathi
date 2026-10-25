package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStorageLifecycle(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_netsathi.db")

	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("failed to initialize db: %v", err)
	}
	defer db.Close()

	// 1. Create transfer
	transfer := &Transfer{
		ID:             "tx-12345",
		Token:          "token-secret",
		OriginalName:   "sample.bin",
		TotalSize:      2048,
		ChunkSize:      1024,
		TotalChunks:    2,
		ExpectedSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Status:         StatusCreated,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}

	if err := db.CreateTransfer(transfer); err != nil {
		t.Fatalf("failed to insert transfer: %v", err)
	}

	// 2. Fetch transfer
	fetched, err := db.GetTransfer("tx-12345")
	if err != nil {
		t.Fatalf("failed to get transfer: %v", err)
	}
	if fetched.OriginalName != "sample.bin" {
		t.Errorf("expected name sample.bin, got %s", fetched.OriginalName)
	}

	// 3. Record chunk 0
	chunkFile := filepath.Join(tempDir, "chunk_0.part")
	if err := os.WriteFile(chunkFile, make([]byte, 1024), 0644); err != nil {
		t.Fatalf("failed to write chunk file: %v", err)
	}

	c0 := &Chunk{
		TransferID: "tx-12345",
		ChunkIndex: 0,
		Size:       1024,
		StoredPath: chunkFile,
		Status:     ChunkStatusReceived,
		CreatedAt:  time.Now().UTC(),
	}
	if err := db.RecordChunk(c0); err != nil {
		t.Fatalf("failed to record chunk: %v", err)
	}

	// 4. Missing chunks check
	missing, err := db.GetMissingChunkIndexes("tx-12345")
	if err != nil {
		t.Fatalf("failed to get missing chunks: %v", err)
	}
	if len(missing) != 1 || missing[0] != 1 {
		t.Fatalf("expected missing chunk [1], got %v", missing)
	}

	// 5. Test reconcile
	reconciled, removed, err := db.ReconcileStartupState()
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	if reconciled != 1 || removed != 0 {
		t.Errorf("expected 1 reconciled and 0 removed, got %d, %d", reconciled, removed)
	}
}
