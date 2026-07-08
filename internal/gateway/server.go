package gateway

import (
	"crypto/rsa"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/zenmind/onlyoffice-gateway/internal/config"
	"github.com/zenmind/onlyoffice-gateway/internal/handler"
	"github.com/zenmind/onlyoffice-gateway/internal/storage"
)

var documentServerHTTPClient = &http.Client{Timeout: 5 * time.Second}

// ServiceResolver provides access to registered services and their public keys.
type ServiceResolver interface {
	Resolve(id string) (*rsa.PublicKey, []string, bool)
}

func NewHandler(cfg *config.Config, resolver ServiceResolver) http.Handler {
	store, err := storage.NewLocalStore(cfg.StorageDir)
	if err != nil {
		panic("failed to create storage: " + err.Error())
	}

	mux := http.NewServeMux()

	mux.Handle("POST /api/v1/documents", handler.NewUploadHandler(cfg, resolver, store))

	mux.HandleFunc("GET /api/v1/documents/{id}", func(w http.ResponseWriter, r *http.Request) {
		handler.NewDownloadHandler(store).ServeHTTP(w, r)
	})

	mux.Handle("POST /callback", handler.NewCallbackHandler(store, cfg.WebhookMaxRetries, cfg.JWTSecret))

	mux.HandleFunc("GET /edit", func(w http.ResponseWriter, r *http.Request) {
		editor := handler.NewEditorHandler(cfg, resolver, store, getServerURL(r))
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

	mux.HandleFunc("GET /api/v1/health/ds", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		dsURL := cfg.DocumentServerURL + "/healthcheck"
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, dsURL, nil)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, `{"document_server_ok":false,"document_server_url":"%s"}`, cfg.DocumentServerURL)
			return
		}
		resp, err := documentServerHTTPClient.Do(req)
		ok := err == nil && resp != nil && resp.StatusCode == http.StatusOK
		if resp != nil {
			resp.Body.Close()
		}
		status := http.StatusOK
		if !ok {
			status = http.StatusServiceUnavailable
		}
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"document_server_ok":%t,"document_server_url":"%s"}`, ok, cfg.DocumentServerURL)
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
