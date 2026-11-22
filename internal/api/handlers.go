package api

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

	// GET /api/transfers/{id} or DELETE /api/transfers/{id}
	case len(segments) == 1:
		if r.Method == http.MethodDelete {
			h.handleDeleteTransfer(w, r, segments[0])
		} else {
			h.handleGetTransfer(w, r, segments[0])
		}

	// GET /api/transfers/{id}/missing
	case len(segments) == 2 && segments[1] == "missing":
		h.handleGetMissingChunks(w, r, segments[0])

	// POST /api/transfers/{id}/complete
	case len(segments) == 2 && segments[1] == "complete":
		h.handleCompleteTransfer(w, r, segments[0])

	// GET /api/transfers/{id}/download
	case len(segments) == 2 && segments[1] == "download":
		h.handleDownloadFile(w, r, segments[0])

	// GET /api/transfers/{id}/preview
	case len(segments) == 2 && segments[1] == "preview":
		h.handlePreviewFile(w, r, segments[0])

	// GET or POST /api/transfers/batch/zip
	case len(segments) == 2 && segments[0] == "batch" && segments[1] == "zip":
		h.handleDownloadBatchZip(w, r)

	// PUT /api/transfers/{id}/chunks/{index}
	case len(segments) == 3 && segments[1] == "chunks":
		h.handleUploadChunk(w, r, segments[0], segments[2])

	// GET /api/transfers/{id}/progress/stream
	case len(segments) == 3 && segments[1] == "progress" && segments[2] == "stream":
		h.handleProgressStream(w, r, segments[0])

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

// DELETE /api/transfers/{id}
func (h *Handler) handleDeleteTransfer(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if err := h.mgr.DeleteTransfer(id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "transfer not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message": "transfer deleted successfully",
		"id":      id,
	})
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

// mimeFromExtension returns an appropriate MIME type for inline browser preview.
func mimeFromExtension(name string) string {
	ext := strings.ToLower(name)
	if i := strings.LastIndex(ext, "."); i >= 0 {
		ext = ext[i:]
	}
	mimes := map[string]string{
		".pdf":  "application/pdf",
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".gif":  "image/gif",
		".webp": "image/webp",
		".svg":  "image/svg+xml",
		".bmp":  "image/bmp",
		".mp4":  "video/mp4",
		".webm": "video/webm",
		".ogg":  "video/ogg",
		".mov":  "video/quicktime",
		".mp3":  "audio/mpeg",
		".wav":  "audio/wav",
		".flac": "audio/flac",
		".aac":  "audio/aac",
		".txt":  "text/plain; charset=utf-8",
		".log":  "text/plain; charset=utf-8",
		".json": "application/json",
		".xml":  "text/xml; charset=utf-8",
		".csv":  "text/csv; charset=utf-8",
		".html": "text/html; charset=utf-8",
		".css":  "text/css; charset=utf-8",
		".js":   "text/javascript; charset=utf-8",
		".go":   "text/plain; charset=utf-8",
		".py":   "text/plain; charset=utf-8",
		".md":   "text/plain; charset=utf-8",
		".c":    "text/plain; charset=utf-8",
		".cpp":  "text/plain; charset=utf-8",
		".java": "text/plain; charset=utf-8",
		".sh":   "text/plain; charset=utf-8",
		".yaml": "text/plain; charset=utf-8",
		".yml":  "text/plain; charset=utf-8",
		".toml": "text/plain; charset=utf-8",
	}
	if mime, ok := mimes[ext]; ok {
		return mime
	}
	return ""
}

// GET /api/transfers/{id}/preview — serves the file inline for in-browser preview.
func (h *Handler) handlePreviewFile(w http.ResponseWriter, r *http.Request, id string) {
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

	mime := mimeFromExtension(originalName)
	if mime == "" {
		// Unsupported preview type — fall back to download
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, originalName))
		w.Header().Set("Content-Type", "application/octet-stream")
	} else {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, originalName))
		w.Header().Set("Content-Type", mime)
	}

	http.ServeFile(w, r, filePath)
}

// GET or POST /api/transfers/batch/zip — streams multiple completed files as a single ZIP archive.
func (h *Handler) handleDownloadBatchZip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var ids []string
	if rawIDs := r.URL.Query().Get("ids"); rawIDs != "" {
		for _, id := range strings.Split(rawIDs, ",") {
			id = strings.TrimSpace(id)
			if id != "" {
				ids = append(ids, id)
			}
		}
	} else if r.Body != nil {
		var req struct {
			IDs []string `json:"ids"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024*1024)).Decode(&req); err == nil {
			ids = req.IDs
		}
	}

	// If no specific IDs provided, zip all completed transfers
	if len(ids) == 0 {
		transfers, err := h.mgr.ListTransfers(100)
		if err == nil {
			for _, t := range transfers {
				if t.Status == storage.StatusCompleted {
					ids = append(ids, t.ID)
				}
			}
		}
	}

	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "no completed files available to zip")
		return
	}

	// Stream ZIP file directly to HTTP client
	zipFilename := fmt.Sprintf("NetSathi_Batch_%s.zip", time.Now().Format("20060102_150405"))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, zipFilename))
	w.Header().Set("Content-Type", "application/zip")

	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()

	addedFiles := make(map[string]int)

	for _, id := range ids {
		filePath, origName, err := h.mgr.GetCompletedFilePath(id)
		if err != nil {
			continue // skip missing or incomplete files
		}

		f, err := os.Open(filePath)
		if err != nil {
			continue
		}

		// Avoid name collisions inside zip
		entryName := origName
		if count, exists := addedFiles[origName]; exists {
			addedFiles[origName] = count + 1
			ext := filepath.Ext(origName)
			base := strings.TrimSuffix(origName, ext)
			entryName = fmt.Sprintf("%s (%d)%s", base, count+1, ext)
		} else {
			addedFiles[origName] = 1
		}

		zf, err := zipWriter.Create(entryName)
		if err != nil {
			f.Close()
			continue
		}

		io.Copy(zf, f)
		f.Close()
	}
}

// GET /api/transfers/{id}/progress/stream — Server-Sent Events real-time progress feed.
func (h *Handler) handleProgressStream(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	token := extractToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "authorization token required")
		return
	}

	// Verify the token without exposing internal DB
	if err := h.mgr.VerifyToken(id, token); err != nil {
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

	// Ensure the ResponseWriter supports flushing (SSE requires it)
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported by server")
		return
	}

	// Set SSE-specific headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable Nginx proxy buffering

	ctx := r.Context()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			return
		case <-ticker.C:
			progress, err := h.mgr.GetTransferProgress(id)
			if err != nil {
				fmt.Fprintf(w, "event: error\ndata: {\"error\":\"transfer not found\"}\n\n")
				flusher.Flush()
				return
			}

			data, _ := json.Marshal(progress)

			switch progress.Transfer.Status {
			case storage.StatusCompleted:
				fmt.Fprintf(w, "event: completed\ndata: %s\n\n", data)
				flusher.Flush()
				return
			case storage.StatusFailed:
				fmt.Fprintf(w, "event: failed\ndata: %s\n\n", data)
				flusher.Flush()
				return
			default:
				fmt.Fprintf(w, "event: progress\ndata: %s\n\n", data)
				flusher.Flush()
			}
		}
	}
}
