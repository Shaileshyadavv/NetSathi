package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// DB encapsulates the SQLite connection pool and synchronization.
type DB struct {
	db *sql.DB
	mu sync.RWMutex
}

// NewDB initializes and migrates the SQLite database at dbPath.
func NewDB(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	// SQLite connection string enabling WAL and foreign keys
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", dbPath)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Recommended SQLite pool settings for concurrent multi-reader/single-writer
	conn.SetMaxOpenConns(10)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(time.Hour)

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping sqlite database: %w", err)
	}

	dbInstance := &DB{db: conn}
	if err := dbInstance.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	return dbInstance, nil
}

// Close closes the underlying database handle.
func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS transfers (
		id TEXT PRIMARY KEY,
		token TEXT NOT NULL,
		original_name TEXT NOT NULL,
		total_size INTEGER NOT NULL,
		chunk_size INTEGER NOT NULL,
		total_chunks INTEGER NOT NULL,
		expected_sha256 TEXT NOT NULL,
		actual_sha256 TEXT DEFAULT '',
		status TEXT NOT NULL,
		stored_path TEXT DEFAULT '',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS chunks (
		transfer_id TEXT NOT NULL,
		chunk_index INTEGER NOT NULL,
		size INTEGER NOT NULL,
		stored_path TEXT NOT NULL,
		status TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		PRIMARY KEY (transfer_id, chunk_index),
		FOREIGN KEY (transfer_id) REFERENCES transfers(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_transfers_status ON transfers(status);
	CREATE INDEX IF NOT EXISTS idx_transfers_updated ON transfers(updated_at DESC);
	CREATE INDEX IF NOT EXISTS idx_chunks_transfer ON chunks(transfer_id);
	`
	_, err := d.db.Exec(schema)
	return err
}

// CreateTransfer inserts a new transfer record.
func (d *DB) CreateTransfer(t *Transfer) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	query := `
	INSERT INTO transfers (
		id, token, original_name, total_size, chunk_size, total_chunks,
		expected_sha256, actual_sha256, status, stored_path, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`
	_, err := d.db.Exec(
		query,
		t.ID, t.Token, t.OriginalName, t.TotalSize, t.ChunkSize, t.TotalChunks,
		t.ExpectedSHA256, t.ActualSHA256, t.Status, t.StoredPath, t.CreatedAt, t.UpdatedAt,
	)
	return err
}

// GetTransfer fetches a transfer record by ID.
func (d *DB) GetTransfer(id string) (*Transfer, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `
	SELECT id, token, original_name, total_size, chunk_size, total_chunks,
	       expected_sha256, actual_sha256, status, stored_path, created_at, updated_at
	FROM transfers WHERE id = ?;
	`
	row := d.db.QueryRow(query, id)

	var t Transfer
	err := row.Scan(
		&t.ID, &t.Token, &t.OriginalName, &t.TotalSize, &t.ChunkSize, &t.TotalChunks,
		&t.ExpectedSHA256, &t.ActualSHA256, &t.Status, &t.StoredPath, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

// ListTransfers fetches all transfers ordered by most recent.
func (d *DB) ListTransfers(limit int) ([]Transfer, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	query := `
	SELECT id, token, original_name, total_size, chunk_size, total_chunks,
	       expected_sha256, actual_sha256, status, stored_path, created_at, updated_at
	FROM transfers ORDER BY updated_at DESC LIMIT ?;
	`
	rows, err := d.db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Transfer
	for rows.Next() {
		var t Transfer
		if err := rows.Scan(
			&t.ID, &t.Token, &t.OriginalName, &t.TotalSize, &t.ChunkSize, &t.TotalChunks,
			&t.ExpectedSHA256, &t.ActualSHA256, &t.Status, &t.StoredPath, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

// UpdateTransferStatus updates the transfer status, actual hash, stored path, and updated_at.
func (d *DB) UpdateTransferStatus(id string, status string, actualHash string, storedPath string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	query := `
	UPDATE transfers
	SET status = ?, actual_sha256 = ?, stored_path = ?, updated_at = ?
	WHERE id = ?;
	`
	res, err := d.db.Exec(query, status, actualHash, storedPath, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordChunk registers or replaces a chunk record in the database.
func (d *DB) RecordChunk(c *Chunk) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	query := `
	INSERT INTO chunks (transfer_id, chunk_index, size, stored_path, status, created_at)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(transfer_id, chunk_index) DO UPDATE SET
		size = excluded.size,
		stored_path = excluded.stored_path,
		status = excluded.status,
		created_at = excluded.created_at;
	`
	_, err := d.db.Exec(query, c.TransferID, c.ChunkIndex, c.Size, c.StoredPath, c.Status, c.CreatedAt)
	if err != nil {
		return err
	}

	// Update transfer updated_at timestamp and set status to Uploading if it was Created
	touchQuery := `
	UPDATE transfers
	SET updated_at = ?, status = CASE WHEN status = 'Created' THEN 'Uploading' ELSE status END
	WHERE id = ?;
	`
	_, _ = d.db.Exec(touchQuery, time.Now().UTC(), c.TransferID)
	return nil
}

// GetReceivedChunks returns all chunks registered for a transfer, ordered by chunk index.
func (d *DB) GetReceivedChunks(transferID string) ([]Chunk, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `
	SELECT transfer_id, chunk_index, size, stored_path, status, created_at
	FROM chunks WHERE transfer_id = ? ORDER BY chunk_index ASC;
	`
	rows, err := d.db.Query(query, transferID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chunks []Chunk
	for rows.Next() {
		var c Chunk
		if err := rows.Scan(&c.TransferID, &c.ChunkIndex, &c.Size, &c.StoredPath, &c.Status, &c.CreatedAt); err != nil {
			return nil, err
		}
		chunks = append(chunks, c)
	}
	return chunks, rows.Err()
}

// GetMissingChunkIndexes returns indexes [0, totalChunks) that have not been recorded.
func (d *DB) GetMissingChunkIndexes(transferID string) ([]int, error) {
	t, err := d.GetTransfer(transferID)
	if err != nil {
		return nil, err
	}

	received, err := d.GetReceivedChunks(transferID)
	if err != nil {
		return nil, err
	}

	receivedMap := make(map[int]bool, len(received))
	for _, c := range received {
		receivedMap[c.ChunkIndex] = true
	}

	var missing []int
	for i := 0; i < t.TotalChunks; i++ {
		if !receivedMap[i] {
			missing = append(missing, i)
		}
	}
	return missing, nil
}

// ReconcileStartupState checks existing chunks in DB against disk, removing orphaned records
// and recovering incomplete transfers (Module 3 & Section 11 of the specification).
func (d *DB) ReconcileStartupState() (reconciled int, removed int, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 1. Check all chunks for active/incomplete transfers
	rows, err := d.db.Query(`
		SELECT c.transfer_id, c.chunk_index, c.size, c.stored_path
		FROM chunks c
		JOIN transfers t ON c.transfer_id = t.id
		WHERE t.status IN ('Created', 'Uploading', 'Verifying');
	`)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	type chunkRef struct {
		transferID string
		chunkIndex int
		storedPath string
		size       int64
	}
	var toCheck []chunkRef
	for rows.Next() {
		var cr chunkRef
		if err := rows.Scan(&cr.transferID, &cr.chunkIndex, &cr.size, &cr.storedPath); err == nil {
			toCheck = append(toCheck, cr)
		}
	}
	rows.Close()

	for _, cr := range toCheck {
		info, statErr := os.Stat(cr.storedPath)
		if statErr != nil || info.Size() != cr.size {
			// Chunk file missing or corrupted on disk, delete record so resume can re-fetch it
			_, _ = d.db.Exec("DELETE FROM chunks WHERE transfer_id = ? AND chunk_index = ?", cr.transferID, cr.chunkIndex)
			removed++
		} else {
			reconciled++
		}
	}

	// 2. Set any transfer stranded in 'Verifying' back to 'Uploading' so client can re-trigger verification/completion
	_, _ = d.db.Exec("UPDATE transfers SET status = 'Uploading' WHERE status = 'Verifying'")

	return reconciled, removed, nil
}

// GetExpiredTransfers finds transfers that have not been updated since cutoff and are not completed.
func (d *DB) GetExpiredTransfers(cutoff time.Time) ([]Transfer, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `
	SELECT id, token, original_name, total_size, chunk_size, total_chunks,
	       expected_sha256, actual_sha256, status, stored_path, created_at, updated_at
	FROM transfers WHERE updated_at < ? AND status != 'Completed';
	`
	rows, err := d.db.Query(query, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Transfer
	for rows.Next() {
		var t Transfer
		if err := rows.Scan(
			&t.ID, &t.Token, &t.OriginalName, &t.TotalSize, &t.ChunkSize, &t.TotalChunks,
			&t.ExpectedSHA256, &t.ActualSHA256, &t.Status, &t.StoredPath, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

// DeleteTransfer deletes a transfer and cascades deletion to associated chunk records.
func (d *DB) DeleteTransfer(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.Exec("DELETE FROM transfers WHERE id = ?", id)
	return err
}

