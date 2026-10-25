package storage

import (
	"errors"
	"time"
)

// Transfer status constants
const (
	StatusCreated   = "Created"
	StatusUploading = "Uploading"
	StatusVerifying = "Verifying"
	StatusCompleted = "Completed"
	StatusFailed    = "Failed"
	StatusExpired   = "Expired"
)

// Chunk status constants
const (
	ChunkStatusReceived = "Received"
	ChunkStatusVerified = "Verified"
)

var (
	ErrNotFound       = errors.New("record not found")
	ErrInvalidStatus  = errors.New("invalid transfer status transition")
	ErrChunkExists    = errors.New("chunk already exists")
	ErrUnauthorized   = errors.New("unauthorized token for transfer")
)

// Transfer represents metadata for a single resumable file transfer session.
type Transfer struct {
	ID             string    `json:"id"`
	Token          string    `json:"token,omitempty"` // High-entropy access token
	OriginalName   string    `json:"original_name"`
	TotalSize      int64     `json:"total_size"`
	ChunkSize      int64     `json:"chunk_size"`
	TotalChunks    int       `json:"total_chunks"`
	ExpectedSHA256 string    `json:"expected_sha256"`
	ActualSHA256   string    `json:"actual_sha256,omitempty"`
	Status         string    `json:"status"`
	StoredPath     string    `json:"stored_path,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Chunk tracks the received status and file system path of an uploaded chunk.
type Chunk struct {
	TransferID string    `json:"transfer_id"`
	ChunkIndex int       `json:"chunk_index"`
	Size       int64     `json:"size"`
	StoredPath string    `json:"stored_path"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

// TransferProgress contains summary info for dashboard and progress tracking.
type TransferProgress struct {
	Transfer        Transfer `json:"transfer"`
	ReceivedChunks  int      `json:"received_chunks"`
	ReceivedBytes   int64    `json:"received_bytes"`
	Percentage      float64  `json:"percentage"`
	MissingChunkIDs []int    `json:"missing_chunk_ids,omitempty"`
}
