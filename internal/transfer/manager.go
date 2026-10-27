package transfer

import (
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"netsathi/internal/integrity"
	"netsathi/internal/security"
	"netsathi/internal/storage"
)

// Manager orchestrates chunk-based file transfers: creation, chunk saving,
// assembly, integrity verification, and atomic file publishing.
type Manager struct {
	db           *storage.DB
	chunkDir     string // temporary chunk storage
	completedDir string // final verified files
}

// NewManager creates a new transfer manager.
func NewManager(db *storage.DB, chunkDir, completedDir string) *Manager {
	return &Manager{
		db:           db,
		chunkDir:     chunkDir,
		completedDir: completedDir,
	}
}

// CreateTransferRequest holds the parameters for initiating a new transfer.
type CreateTransferRequest struct {
	OriginalName   string `json:"original_name"`
	TotalSize      int64  `json:"total_size"`
	ChunkSize      int64  `json:"chunk_size"`
	ExpectedSHA256 string `json:"expected_sha256"`
}

// CreateTransferResponse is returned to the client after a transfer is created.
type CreateTransferResponse struct {
	ID          string `json:"id"`
	Token       string `json:"token"`
	TotalChunks int    `json:"total_chunks"`
	ChunkSize   int64  `json:"chunk_size"`
}

// Validate checks the create request for required fields and sane values.
func (r *CreateTransferRequest) Validate() error {
	if r.OriginalName == "" {
		return fmt.Errorf("original_name is required")
	}
	if r.TotalSize <= 0 {
		return fmt.Errorf("total_size must be positive")
	}
	if r.ChunkSize <= 0 {
		return fmt.Errorf("chunk_size must be positive")
	}
	if r.ChunkSize > 10*1024*1024 { // 10 MB max chunk size
		return fmt.Errorf("chunk_size cannot exceed 10 MB")
	}
	if r.ExpectedSHA256 == "" {
		return fmt.Errorf("expected_sha256 is required")
	}
	if len(r.ExpectedSHA256) != 64 {
		return fmt.Errorf("expected_sha256 must be a 64-character hex string")
	}
	return nil
}

// CreateTransfer initializes a new resumable file transfer session.
func (m *Manager) CreateTransfer(req *CreateTransferRequest) (*CreateTransferResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validation error: %w", err)
	}

	id := uuid.New().String()
	token, err := security.GenerateSecureToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	totalChunks := int(math.Ceil(float64(req.TotalSize) / float64(req.ChunkSize)))
	safeName := security.SanitizeFilename(req.OriginalName)

	// Create a dedicated directory for this transfer's chunks
	transferChunkDir := filepath.Join(m.chunkDir, id)
	if err := os.MkdirAll(transferChunkDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create transfer chunk dir: %w", err)
	}

	now := time.Now().UTC()
	transfer := &storage.Transfer{
		ID:             id,
		Token:          token,
		OriginalName:   safeName,
		TotalSize:      req.TotalSize,
		ChunkSize:      req.ChunkSize,
		TotalChunks:    totalChunks,
		ExpectedSHA256: req.ExpectedSHA256,
		Status:         storage.StatusCreated,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := m.db.CreateTransfer(transfer); err != nil {
		return nil, fmt.Errorf("failed to persist transfer: %w", err)
	}

	log.Printf("[INFO] Transfer created: id=%s name=%s size=%d chunks=%d", id, safeName, req.TotalSize, totalChunks)

	return &CreateTransferResponse{
		ID:          id,
		Token:       token,
		TotalChunks: totalChunks,
		ChunkSize:   req.ChunkSize,
	}, nil
}

// SaveChunk writes a chunk's data to disk and registers it in the database.
// It is idempotent — re-uploading an existing chunk index replaces it.
func (m *Manager) SaveChunk(transferID string, token string, chunkIndex int, data io.Reader) (int64, error) {
	// 1. Verify transfer exists and token matches
	transfer, err := m.db.GetTransfer(transferID)
	if err != nil {
		return 0, fmt.Errorf("transfer not found: %w", err)
	}
	if transfer.Token != token {
		return 0, storage.ErrUnauthorized
	}

	// 2. Validate transfer is in a state that accepts chunks
	if transfer.Status != storage.StatusCreated && transfer.Status != storage.StatusUploading {
		return 0, fmt.Errorf("transfer status is '%s', cannot accept chunks", transfer.Status)
	}

	// 3. Validate chunk index range
	if chunkIndex < 0 || chunkIndex >= transfer.TotalChunks {
		return 0, fmt.Errorf("chunk index %d out of range [0, %d)", chunkIndex, transfer.TotalChunks)
	}

	// 4. Write chunk to disk with a temp file then rename (atomic)
	transferChunkDir := filepath.Join(m.chunkDir, transferID)
	if err := os.MkdirAll(transferChunkDir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create chunk dir: %w", err)
	}

	chunkPath := filepath.Join(transferChunkDir, fmt.Sprintf("chunk_%d.part", chunkIndex))
	tmpPath := chunkPath + ".tmp"

	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return 0, fmt.Errorf("failed to create temp chunk file: %w", err)
	}

	written, err := io.Copy(tmpFile, data)
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return 0, fmt.Errorf("failed to write chunk data: %w", err)
	}
	tmpFile.Close()

	// Atomic rename from .tmp to final
	if err := os.Rename(tmpPath, chunkPath); err != nil {
		os.Remove(tmpPath)
		return 0, fmt.Errorf("failed to finalize chunk file: %w", err)
	}

	// 5. Record the chunk in the database
	chunk := &storage.Chunk{
		TransferID: transferID,
		ChunkIndex: chunkIndex,
		Size:       written,
		StoredPath: chunkPath,
		Status:     storage.ChunkStatusReceived,
		CreatedAt:  time.Now().UTC(),
	}
	if err := m.db.RecordChunk(chunk); err != nil {
		return 0, fmt.Errorf("failed to record chunk: %w", err)
	}

	log.Printf("[INFO] Chunk saved: transfer=%s index=%d size=%d", transferID, chunkIndex, written)
	return written, nil
}

// GetMissingChunks returns the list of chunk indexes that have not yet been uploaded.
func (m *Manager) GetMissingChunks(transferID string, token string) ([]int, error) {
	transfer, err := m.db.GetTransfer(transferID)
	if err != nil {
		return nil, fmt.Errorf("transfer not found: %w", err)
	}
	if transfer.Token != token {
		return nil, storage.ErrUnauthorized
	}

	return m.db.GetMissingChunkIndexes(transferID)
}

// GetTransferProgress returns transfer details along with upload progress info.
func (m *Manager) GetTransferProgress(transferID string) (*storage.TransferProgress, error) {
	transfer, err := m.db.GetTransfer(transferID)
	if err != nil {
		return nil, err
	}

	chunks, err := m.db.GetReceivedChunks(transferID)
	if err != nil {
		return nil, err
	}

	var receivedBytes int64
	for _, c := range chunks {
		receivedBytes += c.Size
	}

	percentage := 0.0
	if transfer.TotalChunks > 0 {
		percentage = float64(len(chunks)) / float64(transfer.TotalChunks) * 100.0
	}

	missing, _ := m.db.GetMissingChunkIndexes(transferID)

	// Strip the token from the response for security
	transfer.Token = ""

	return &storage.TransferProgress{
		Transfer:        *transfer,
		ReceivedChunks:  len(chunks),
		ReceivedBytes:   receivedBytes,
		Percentage:      math.Round(percentage*100) / 100,
		MissingChunkIDs: missing,
	}, nil
}

// CompleteTransfer assembles all chunks into the final file, verifies the SHA-256
// hash, and atomically publishes the file (or marks it as failed).
func (m *Manager) CompleteTransfer(transferID string, token string) (*storage.TransferProgress, error) {
	// 1. Verify transfer exists and token matches
	transfer, err := m.db.GetTransfer(transferID)
	if err != nil {
		return nil, fmt.Errorf("transfer not found: %w", err)
	}
	if transfer.Token != token {
		return nil, storage.ErrUnauthorized
	}

	// 2. Check all chunks are present
	missing, err := m.db.GetMissingChunkIndexes(transferID)
	if err != nil {
		return nil, err
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("cannot complete: %d chunks still missing (indexes: %v)", len(missing), missing)
	}

	// 3. Set status to Verifying
	if err := m.db.UpdateTransferStatus(transferID, storage.StatusVerifying, "", ""); err != nil {
		return nil, err
	}

	// 4. Assemble chunks into a temporary file
	chunks, err := m.db.GetReceivedChunks(transferID)
	if err != nil {
		return nil, err
	}

	tmpAssembledPath := filepath.Join(m.completedDir, transferID+".tmp")
	assembledFile, err := os.Create(tmpAssembledPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create assembled file: %w", err)
	}

	for _, chunk := range chunks {
		chunkFile, err := os.Open(chunk.StoredPath)
		if err != nil {
			assembledFile.Close()
			os.Remove(tmpAssembledPath)
			m.db.UpdateTransferStatus(transferID, storage.StatusFailed, "", "")
			return nil, fmt.Errorf("failed to read chunk %d: %w", chunk.ChunkIndex, err)
		}
		if _, err := io.Copy(assembledFile, chunkFile); err != nil {
			chunkFile.Close()
			assembledFile.Close()
			os.Remove(tmpAssembledPath)
			m.db.UpdateTransferStatus(transferID, storage.StatusFailed, "", "")
			return nil, fmt.Errorf("failed to write chunk %d to assembled file: %w", chunk.ChunkIndex, err)
		}
		chunkFile.Close()
	}
	assembledFile.Close()

	// 5. Verify SHA-256 integrity
	matched, actualHash, err := integrity.VerifyFileSHA256(tmpAssembledPath, transfer.ExpectedSHA256)
	if err != nil {
		os.Remove(tmpAssembledPath)
		m.db.UpdateTransferStatus(transferID, storage.StatusFailed, "", "")
		return nil, fmt.Errorf("SHA-256 verification failed: %w", err)
	}

	if !matched {
		os.Remove(tmpAssembledPath)
		m.db.UpdateTransferStatus(transferID, storage.StatusFailed, actualHash, "")
		log.Printf("[ERROR] Hash mismatch for transfer %s: expected=%s actual=%s", transferID, transfer.ExpectedSHA256, actualHash)
		return nil, fmt.Errorf("integrity check failed: expected SHA-256 %s, got %s", transfer.ExpectedSHA256, actualHash)
	}

	// 6. Atomic publish: rename from .tmp to final path (UUID-based, not user filename)
	finalPath := filepath.Join(m.completedDir, transferID+"_"+transfer.OriginalName)
	if err := os.Rename(tmpAssembledPath, finalPath); err != nil {
		os.Remove(tmpAssembledPath)
		m.db.UpdateTransferStatus(transferID, storage.StatusFailed, actualHash, "")
		return nil, fmt.Errorf("failed to publish completed file: %w", err)
	}

	// 7. Update database
	if err := m.db.UpdateTransferStatus(transferID, storage.StatusCompleted, actualHash, finalPath); err != nil {
		return nil, err
	}

	// 8. Clean up chunk files (fire-and-forget best effort)
	go m.cleanupChunks(transferID)

	log.Printf("[INFO] Transfer completed: id=%s name=%s hash=%s", transferID, transfer.OriginalName, actualHash)

	return m.GetTransferProgress(transferID)
}

// cleanupChunks removes the chunk directory for a completed transfer.
func (m *Manager) cleanupChunks(transferID string) {
	chunkDir := filepath.Join(m.chunkDir, transferID)
	if err := os.RemoveAll(chunkDir); err != nil {
		log.Printf("[WARN] Failed to clean up chunks for %s: %v", transferID, err)
	} else {
		log.Printf("[INFO] Cleaned up chunks for transfer %s", transferID)
	}
}

// ListTransfers returns recent transfers.
func (m *Manager) ListTransfers(limit int) ([]storage.Transfer, error) {
	transfers, err := m.db.ListTransfers(limit)
	if err != nil {
		return nil, err
	}
	// Strip tokens from response for security
	for i := range transfers {
		transfers[i].Token = ""
	}
	return transfers, nil
}

// GetCompletedFilePath returns the on-disk path for a completed transfer's file.
func (m *Manager) GetCompletedFilePath(transferID string) (string, string, error) {
	transfer, err := m.db.GetTransfer(transferID)
	if err != nil {
		return "", "", err
	}
	if transfer.Status != storage.StatusCompleted {
		return "", "", fmt.Errorf("transfer is not completed (status: %s)", transfer.Status)
	}
	if transfer.StoredPath == "" {
		return "", "", fmt.Errorf("completed file path not found")
	}
	return transfer.StoredPath, transfer.OriginalName, nil
}
