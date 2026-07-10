package gateway

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
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
	store, err := storage.NewStore(context.Background(), cfg)
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
		serveOriginalDocument(w, r, store, docID)
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

func serveOriginalDocument(w http.ResponseWriter, r *http.Request, store storage.Store, docID string) {
	meta, err := store.GetMeta(r.Context(), docID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	reader, _, info, err := store.Open(r.Context(), docID, storage.VariantOriginal, nil)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	reader.Close()

	etag := `"` + meta.EditorKey + `"`
	lastModified := meta.CreatedAt.UTC()
	if lastModified.IsZero() && info != nil {
		lastModified = info.LastModified.UTC()
	}
	if checkNotModified(w, r, etag, lastModified) {
		return
	}

	byteRange, status := parseRangeHeader(r.Header.Get("Range"), info.Size)
	if status == http.StatusRequestedRangeNotSatisfiable {
		setOriginalHeaders(w, meta, info, etag, lastModified)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", info.Size))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}

	reader, _, info, err = store.Open(r.Context(), docID, storage.VariantOriginal, byteRange)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidRange) {
			setOriginalHeaders(w, meta, info, etag, lastModified)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", info.Size))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer reader.Close()

	setOriginalHeaders(w, meta, info, etag, lastModified)
	if byteRange == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
		w.WriteHeader(http.StatusOK)
		io.Copy(w, reader)
		return
	}
	end := byteRange.End
	if end >= info.Size {
		end = info.Size - 1
	}
	length := end - byteRange.Start + 1
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", byteRange.Start, end, info.Size))
	w.WriteHeader(http.StatusPartialContent)
	io.Copy(w, reader)
}

func setOriginalHeaders(w http.ResponseWriter, meta *storage.Meta, info *storage.ObjectInfo, etag string, lastModified time.Time) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "private, max-age=28800")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("ETag", etag)
	if !lastModified.IsZero() {
		w.Header().Set("Last-Modified", lastModified.Format(http.TimeFormat))
	}
	if meta != nil && meta.FileName != "" {
		w.Header().Set("Content-Disposition", "inline; filename="+meta.FileName)
	}
	if info != nil && info.ContentType != "" {
		w.Header().Set("Content-Type", info.ContentType)
	}
}

func checkNotModified(w http.ResponseWriter, r *http.Request, etag string, lastModified time.Time) bool {
	if match := r.Header.Get("If-None-Match"); match != "" {
		for _, candidate := range strings.Split(match, ",") {
			if strings.TrimSpace(candidate) == etag || strings.TrimSpace(candidate) == "*" {
				w.Header().Set("ETag", etag)
				w.WriteHeader(http.StatusNotModified)
				return true
			}
		}
	}
	if sinceHeader := r.Header.Get("If-Modified-Since"); sinceHeader != "" && !lastModified.IsZero() {
		if since, err := http.ParseTime(sinceHeader); err == nil && !lastModified.After(since) {
			w.Header().Set("ETag", etag)
			w.Header().Set("Last-Modified", lastModified.Format(http.TimeFormat))
			w.WriteHeader(http.StatusNotModified)
			return true
		}
	}
	return false
}

func parseRangeHeader(header string, size int64) (*storage.ByteRange, int) {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil, http.StatusOK
	}
	if !strings.HasPrefix(header, "bytes=") || strings.Contains(header, ",") {
		return nil, http.StatusRequestedRangeNotSatisfiable
	}
	if size <= 0 {
		return nil, http.StatusRequestedRangeNotSatisfiable
	}
	spec := strings.TrimPrefix(header, "bytes=")
	startText, endText, ok := strings.Cut(spec, "-")
	if !ok {
		return nil, http.StatusRequestedRangeNotSatisfiable
	}
	if startText == "" {
		suffix, err := strconv.ParseInt(endText, 10, 64)
		if err != nil || suffix <= 0 {
			return nil, http.StatusRequestedRangeNotSatisfiable
		}
		if suffix > size {
			suffix = size
		}
		return &storage.ByteRange{Start: size - suffix, End: size - 1}, http.StatusPartialContent
	}
	start, err := strconv.ParseInt(startText, 10, 64)
	if err != nil || start < 0 || start >= size {
		return nil, http.StatusRequestedRangeNotSatisfiable
	}
	end := size - 1
	if endText != "" {
		parsedEnd, err := strconv.ParseInt(endText, 10, 64)
		if err != nil || parsedEnd < start {
			return nil, http.StatusRequestedRangeNotSatisfiable
		}
		end = parsedEnd
		if end >= size {
			end = size - 1
		}
	}
	return &storage.ByteRange{Start: start, End: end}, http.StatusPartialContent
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
		strings.HasPrefix(path, "/spellchecker/"),
		strings.HasPrefix(path, "/cache/"):
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
