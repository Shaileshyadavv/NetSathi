package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"netsathi/internal/storage"
	"netsathi/internal/transfer"
)

// Handler holds the transfer manager and exposes HTTP handler methods.
type Handler struct {
	mgr *transfer.Manager
}

// NewHandler creates a new API handler.
func NewHandler(mgr *transfer.Manager) *Handler {
	return &Handler{mgr: mgr}
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// extractToken reads the Bearer token from the Authorization header.
func extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	// Also allow token as a query parameter (for download links in browser)
	return r.URL.Query().Get("token")
}

// --- Route helpers ---
// parsePathSegments extracts segments from a path like /api/transfers/{id}/chunks/{index}
func parsePathSegments(path, prefix string) []string {
	trimmed := strings.TrimPrefix(path, prefix)
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// --- Handlers ---

// HandleCreateTransfer handles POST /api/transfers
func (h *Handler) HandleCreateTransfer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req transfer.CreateTransferRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	resp, err := h.mgr.CreateTransfer(&req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, resp)
}

// HandleListTransfers handles GET /api/transfers
func (h *Handler) HandleListTransfers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	transfers, err := h.mgr.ListTransfers(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list transfers")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"transfers": transfers,
		"count":     len(transfers),
	})
}

// HandleTransfers is the top-level router for /api/transfers/...
// It dispatches based on the URL sub-path segments.
func (h *Handler) HandleTransfers(w http.ResponseWriter, r *http.Request) {
	segments := parsePathSegments(r.URL.Path, "/api/transfers")

	switch {
	// POST /api/transfers  or  GET /api/transfers
	case len(segments) == 0:
		if r.Method == http.MethodPost {
			h.HandleCreateTransfer(w, r)
		} else if r.Method == http.MethodGet {
			h.HandleListTransfers(w, r)
		} else {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}

	// GET /api/transfers/{id}
	case len(segments) == 1:
		h.handleGetTransfer(w, r, segments[0])

	// GET /api/transfers/{id}/missing
	case len(segments) == 2 && segments[1] == "missing":
		h.handleGetMissingChunks(w, r, segments[0])

	// POST /api/transfers/{id}/complete
	case len(segments) == 2 && segments[1] == "complete":
		h.handleCompleteTransfer(w, r, segments[0])

	// GET /api/transfers/{id}/download
	case len(segments) == 2 && segments[1] == "download":
		h.handleDownloadFile(w, r, segments[0])

	// PUT /api/transfers/{id}/chunks/{index}
	case len(segments) == 3 && segments[1] == "chunks":
		h.handleUploadChunk(w, r, segments[0], segments[2])

	default:
		writeError(w, http.StatusNotFound, "endpoint not found")
	}
}

// GET /api/transfers/{id}
func (h *Handler) handleGetTransfer(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	progress, err := h.mgr.GetTransferProgress(id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "transfer not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, progress)
}

// GET /api/transfers/{id}/missing
func (h *Handler) handleGetMissingChunks(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	token := extractToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "authorization token required")
		return
	}

	missing, err := h.mgr.GetMissingChunks(id, token)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "transfer not found")
			return
		}
		if errors.Is(err, storage.ErrUnauthorized) {
			writeError(w, http.StatusForbidden, "invalid token")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"transfer_id":    id,
		"missing_chunks": missing,
		"missing_count":  len(missing),
	})
}

// PUT /api/transfers/{id}/chunks/{index}
func (h *Handler) handleUploadChunk(w http.ResponseWriter, r *http.Request, id, indexStr string) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	token := extractToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "authorization token required")
		return
	}

	chunkIndex, err := strconv.Atoi(indexStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chunk index")
		return
	}

	chunkChecksum := r.Header.Get("X-Chunk-SHA256")

	// Limit chunk body to 11 MB (chunk_size max is 10 MB + overhead)
	bodyReader := io.LimitReader(r.Body, 11*1024*1024)
	written, err := h.mgr.SaveChunk(id, token, chunkIndex, chunkChecksum, bodyReader)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "transfer not found")
			return
		}
		if errors.Is(err, storage.ErrUnauthorized) {
			writeError(w, http.StatusForbidden, "invalid token")
			return
		}
		log.Printf("[ERROR] Chunk upload failed: %v", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"transfer_id": id,
		"chunk_index": chunkIndex,
		"bytes_saved": written,
		"status":      "received",
	})
}

// POST /api/transfers/{id}/complete
func (h *Handler) handleCompleteTransfer(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	token := extractToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "authorization token required")
		return
	}

	progress, err := h.mgr.CompleteTransfer(id, token)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "transfer not found")
			return
		}
		if errors.Is(err, storage.ErrUnauthorized) {
			writeError(w, http.StatusForbidden, "invalid token")
			return
		}
		log.Printf("[ERROR] Complete failed for %s: %v", id, err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":  "transfer completed and verified",
		"progress": progress,
	})
}

// GET /api/transfers/{id}/download
func (h *Handler) handleDownloadFile(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	filePath, originalName, err := h.mgr.GetCompletedFilePath(id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "transfer not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, originalName))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, filePath)
}
