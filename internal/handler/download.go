package handler

import (
	"io"
	"mime"
	"net/http"
	"strconv"
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

	meta, err := h.store.GetMeta(r.Context(), documentID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "document not found"})
		return
	}

	if !meta.IsEdited {
		writeJSON(w, http.StatusConflict, map[string]string{"status": "editing", "message": "document is still being edited"})
		return
	}

	reader, _, info, err := h.store.Open(r.Context(), documentID, storage.VariantLatest, nil)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "document file not found"})
		return
	}
	defer reader.Close()

	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": meta.FileName}))
	w.Header().Set("Content-Type", "application/octet-stream")
	if info != nil && info.Size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	}
	io.Copy(w, reader)
}
