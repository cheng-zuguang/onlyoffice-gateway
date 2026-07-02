package gateway

import (
	"io"
	"net/http"

	"github.com/zenmind/onlyoffice-gateway/internal/config"
	"github.com/zenmind/onlyoffice-gateway/internal/handler"
	"github.com/zenmind/onlyoffice-gateway/internal/storage"
)

func NewHandler(cfg *config.Config) http.Handler {
	store, err := storage.NewLocalStore(cfg.StorageDir)
	if err != nil {
		panic("failed to create storage: " + err.Error())
	}

	mux := http.NewServeMux()

	mux.Handle("POST /api/v1/documents", handler.NewUploadHandler(cfg, store))

	mux.HandleFunc("GET /api/v1/documents/{id}", func(w http.ResponseWriter, r *http.Request) {
		handler.NewDownloadHandler(store).ServeHTTP(w, r)
	})

	mux.Handle("POST /callback", handler.NewCallbackHandler(store, cfg.WebhookMaxRetries, cfg.JWTSecret))

	mux.HandleFunc("GET /edit", func(w http.ResponseWriter, r *http.Request) {
		editor := handler.NewEditorHandler(cfg, store, getServerURL(r))
		editor.ServeHTTP(w, r)
	})

	mux.HandleFunc("GET /download/{docId}", func(w http.ResponseWriter, r *http.Request) {
		docID := r.PathValue("docId")
		reader, err := store.Get(docID)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		defer reader.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		io.Copy(w, reader)
	})

	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	return mux
}

func getServerURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
