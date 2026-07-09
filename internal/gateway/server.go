package gateway

import (
	"crypto/rsa"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
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

	registerDocumentServerProxy(mux, cfg.DocumentServerURL)

	return mux
}

func registerDocumentServerProxy(mux *http.ServeMux, documentServerURL string) {
	documentServerURL = strings.TrimSpace(documentServerURL)
	if documentServerURL == "" {
		return
	}
	target, err := url.Parse(documentServerURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.Request == nil || resp.Request.URL == nil {
			return nil
		}
		if cacheControl := documentServerProxyCacheControl(resp.Request.URL.Path); cacheControl != "" {
			resp.Header.Set("Cache-Control", cacheControl)
		}
		return nil
	}
	versionedProxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isDocumentServerVersionedAssetPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	})
	prefixes := []string{
		"/web-apps/",
		"/sdkjs/",
		"/coauthoring/",
		"/spellchecker/",
		"/cache/",
		"/doc/",
		"/healthcheck",
	}
	for _, prefix := range prefixes {
		mux.Handle(prefix, proxy)
	}
	mux.Handle("/", versionedProxy)
}

func documentServerProxyCacheControl(path string) string {
	if isDocumentServerVersionedAssetPath(path) {
		return "public, max-age=31536000, immutable"
	}
	if path == "/web-apps/apps/api/documents/api.js" {
		return "public, max-age=300, stale-while-revalidate=86400"
	}
	if isDocumentServerStaticAssetPath(path) {
		return "public, max-age=86400"
	}
	return ""
}

func isDocumentServerStaticAssetPath(path string) bool {
	switch {
	case strings.HasPrefix(path, "/web-apps/"),
		strings.HasPrefix(path, "/sdkjs/"),
		strings.HasPrefix(path, "/spellchecker/"):
	default:
		return false
	}

	lowerPath := strings.ToLower(path)
	for _, suffix := range []string{
		".css",
		".eot",
		".gif",
		".html",
		".ico",
		".jpg",
		".jpeg",
		".js",
		".json",
		".map",
		".png",
		".svg",
		".ttf",
		".wasm",
		".woff",
		".woff2",
	} {
		if strings.HasSuffix(lowerPath, suffix) {
			return true
		}
	}
	return false
}

func isDocumentServerVersionedAssetPath(path string) bool {
	path = strings.TrimPrefix(path, "/")
	segment, _, ok := strings.Cut(path, "/")
	if !ok || segment == "" {
		return false
	}
	hasDigit := false
	for _, r := range segment {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '.' || r == '-':
		default:
			return false
		}
	}
	return hasDigit
}

func getServerURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwardedProto := firstForwardedValue(r.Header.Get("X-Forwarded-Proto")); forwardedProto == "http" || forwardedProto == "https" {
		scheme = forwardedProto
	}
	host := r.Host
	if forwardedHost := firstForwardedValue(r.Header.Get("X-Forwarded-Host")); forwardedHost != "" {
		host = forwardedHost
	}
	return scheme + "://" + host
}

func firstForwardedValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if index := strings.Index(value, ","); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(value)
}
