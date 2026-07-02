package handler

import (
	"io"
	"net/http"
	"strings"

	"github.com/zenmind/onlyoffice-gateway/internal/storage"
)

type DownloadHandler struct {
	store storage.Store
}

func NewDownloadHandler(store storage.Store) *DownloadHandler {
	return &DownloadHandler{store: store}
}

func (h *DownloadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract document_id from path: /api/v1/documents/{id}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/documents/")
	documentID := strings.Split(path, "/")[0]
	if documentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing document_id"})
		return
	}

	meta, err := h.store.GetMeta(documentID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "document not found"})
		return
	}

	if !meta.IsEdited {
		writeJSON(w, http.StatusConflict, map[string]string{"status": "editing", "message": "document is still being edited"})
		return
	}

	reader, err := h.store.Get(documentID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "document file not found"})
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Disposition", "attachment; filename="+meta.FileName)
	w.Header().Set("Content-Type", "application/octet-stream")
	io.Copy(w, reader)
}
