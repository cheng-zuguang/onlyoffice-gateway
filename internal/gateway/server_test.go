package gateway_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/zenmind/onlyoffice-gateway/internal/admin"
	"github.com/zenmind/onlyoffice-gateway/internal/config"
	"github.com/zenmind/onlyoffice-gateway/internal/gateway"
	"github.com/zenmind/onlyoffice-gateway/internal/storage"
)

func generateRSAKeyPair(t *testing.T) (privatePEM, publicPEM string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	privBytes := x509.MarshalPKCS1PrivateKey(key)
	privBlock := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: privBytes})
	pubBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	pubBlock := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes})
	return string(privBlock), string(pubBlock)
}

func signUploadJWT(t *testing.T, privateKeyPEM string, claims jwt.MapClaims) string {
	t.Helper()
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		t.Fatal("failed to decode private key PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return signed
}

func setupGateway(t *testing.T, privPEM, pubPEM string, whitelist []string) (*httptest.Server, string, *admin.InMemoryServiceStore) {
	return setupGatewayWithMaxUploadBytes(t, privPEM, pubPEM, whitelist, 100<<20)
}

func setupGatewayWithMaxUploadBytes(t *testing.T, privPEM, pubPEM string, whitelist []string, maxUploadBytes int64) (*httptest.Server, string, *admin.InMemoryServiceStore) {
	t.Helper()
	tmpDir := t.TempDir()
	storageDir := filepath.Join(tmpDir, "storage")

	cfg := &config.Config{
		ListenAddr:        "127.0.0.1:18080",
		DocumentServerURL: "https://doc.example.com",
		JWTSecret:         "test-gateway-jwt-secret",
		StorageDir:        storageDir,
		MaxUploadBytes:    maxUploadBytes,
		TTLHours:          8,
		WebhookMaxRetries: 3,
	}
	loaded, _ := config.FromLiteral(cfg)

	store := admin.NewInMemoryServiceStore()
	store.Add(admin.ServiceRecord{
		ID:                    "test-service",
		PublicKeyPEM:          pubPEM,
		AllowedWebhookDomains: whitelist,
	})

	handler := gateway.NewHandler(loaded, store)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server, storageDir, store
}

func callbackURL(serverURL, documentID string) string {
	mac := hmac.New(sha256.New, []byte("test-gateway-jwt-secret"))
	mac.Write([]byte("callback:" + documentID))
	return serverURL + "/callback?token=" + hex.EncodeToString(mac.Sum(nil))
}

// Tracer Bullet: business service uploads a document with valid JWT,
// Gateway stores it, returns 201 + document_id.
func TestUploadDocument(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	uploadJWT := signUploadJWT(t, privPEM, jwt.MapClaims{
		"service_id":  "test-service",
		"webhook_url": "https://test.example.com/callback",
		"external_id": "doc-ext-001",
		"user": map[string]interface{}{
			"id":   "u-1",
			"name": "Alice",
		},
		"file_name":     "test.docx",
		"document_type": "word",
		"exp":           time.Now().Add(60 * time.Second).Unix(),
		"iat":           time.Now().Unix(),
	})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "test.docx")
	io.WriteString(part, "fake-docx-content")
	writer.Close()

	req, _ := http.NewRequest("POST", server.URL+"/api/v1/documents", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+uploadJWT)
	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201 Created, got %d\nbody: %s", resp.StatusCode, string(respBody))
	}

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)
	docID, _ := result["document_id"].(string)
	if docID == "" {
		t.Fatalf("expected document_id in upload response, got %s", string(respBody))
	}

	downloadResp, err := http.Get(server.URL + "/download/" + docID)
	if err != nil {
		t.Fatalf("download original: %v", err)
	}
	defer downloadResp.Body.Close()
	downloaded, _ := io.ReadAll(downloadResp.Body)
	if downloadResp.StatusCode != http.StatusOK {
		t.Fatalf("expected original download 200, got %d: %s", downloadResp.StatusCode, string(downloaded))
	}
	if string(downloaded) != "fake-docx-content" {
		t.Fatalf("expected original content, got %q", string(downloaded))
	}
}

func TestCreateDocumentFromSignedSourceURLWithoutUploadingBytes(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, storageDir, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})
	sourceURL := "https://bucket.s3.example.com/report.docx?X-Amz-Signature=abc"
	token := signUploadJWT(t, privPEM, jwt.MapClaims{
		"service_id": "test-service", "webhook_url": "https://test.example.com/callback",
		"source_url": sourceURL, "file_name": "report.docx", "document_type": "word",
		"exp": time.Now().Add(time.Minute).Unix(),
	})
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/documents", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create direct document: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var result struct {
		DocumentID string `json:"document_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if _, err := os.Stat(filepath.Join(storageDir, result.DocumentID, "original")); !os.IsNotExist(err) {
		t.Fatalf("direct source must not write an original file, err=%v", err)
	}
	editToken := signJWT(t, privPEM, jwt.MapClaims{"service_id": "test-service", "document_id": result.DocumentID, "exp": time.Now().Add(time.Minute).Unix()})
	editResp, err := http.Get(server.URL + "/edit?token=" + editToken)
	if err != nil {
		t.Fatalf("open editor: %v", err)
	}
	defer editResp.Body.Close()
	editorHTML, _ := io.ReadAll(editResp.Body)
	if !strings.Contains(string(editorHTML), sourceURL) {
		t.Fatal("editor config must use the signed source URL directly")
	}
}

func TestDirectSourceRejectsNonHTTPSURL(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})
	token := signUploadJWT(t, privPEM, jwt.MapClaims{"service_id": "test-service", "webhook_url": "https://test.example.com/callback", "source_url": "http://127.0.0.1/private", "document_type": "word", "exp": time.Now().Add(time.Minute).Unix()})
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/documents", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestDirectSourceCallbackForwardsEditedURLWithoutDownloading(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)

	editedURLRequested := make(chan struct{}, 1)
	editedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		editedURLRequested <- struct{}{}
		w.Write([]byte("gateway-must-not-download-this"))
	}))
	defer editedServer.Close()

	webhookPayloads := make(chan map[string]interface{}, 1)
	webhookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode webhook payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		webhookPayloads <- payload
		w.WriteHeader(http.StatusOK)
	}))
	defer webhookServer.Close()

	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"127.0.0.1"})
	sourceURL := "https://bucket.s3.example.com/report.docx?X-Amz-Signature=abc"
	token := signUploadJWT(t, privPEM, jwt.MapClaims{
		"service_id": "test-service", "webhook_url": webhookServer.URL + "/callback",
		"source_url": sourceURL, "file_name": "report.docx", "document_type": "word",
		"external_id": "business-doc-7",
		"exp":         time.Now().Add(time.Minute).Unix(),
	})
	createReq, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/documents", nil)
	createReq.Header.Set("Authorization", "Bearer "+token)
	createResp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		t.Fatalf("create direct document: %v", err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(createResp.Body)
		t.Fatalf("expected 201, got %d: %s", createResp.StatusCode, string(body))
	}
	var created struct {
		DocumentID string `json:"document_id"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	callbackBody := bytes.NewReader(toJSON(map[string]interface{}{
		"status": 2,
		"key":    created.DocumentID,
		"url":    editedServer.URL + "/onlyoffice-cache/report.docx",
	}))
	callbackReq, _ := http.NewRequest(http.MethodPost, callbackURL(server.URL, created.DocumentID), callbackBody)
	callbackReq.Header.Set("Content-Type", "application/json")
	callbackResp, err := http.DefaultClient.Do(callbackReq)
	if err != nil {
		t.Fatalf("post callback: %v", err)
	}
	defer callbackResp.Body.Close()
	if callbackResp.StatusCode != http.StatusOK {
		t.Fatalf("expected callback 200, got %d", callbackResp.StatusCode)
	}

	select {
	case payload := <-webhookPayloads:
		if payload["edited_url"] != editedServer.URL+"/onlyoffice-cache/report.docx" {
			t.Fatalf("expected edited_url to be forwarded, got %#v", payload["edited_url"])
		}
		if payload["external_id"] != "business-doc-7" {
			t.Fatalf("expected external_id to be preserved, got %#v", payload["external_id"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected business webhook to receive edited_url")
	}

	select {
	case <-editedURLRequested:
		t.Fatal("direct source mode must not download the edited URL")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestUploadRejectsOversizedRequestWithoutPersistingDocument(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, storageDir, _ := setupGatewayWithMaxUploadBytes(t, privPEM, pubPEM, []string{"test.example.com"}, 32)

	token := signUploadJWT(t, privPEM, jwt.MapClaims{
		"service_id": "test-service", "webhook_url": "https://test.example.com/callback",
		"file_name": "large.docx", "document_type": "word",
		"exp": time.Now().Add(time.Minute).Unix(),
	})
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("file", "large.docx")
	_, _ = io.WriteString(part, "this document exceeds the configured limit")
	_ = form.Close()

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/documents", &body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", form.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized upload, got %d", resp.StatusCode)
	}
	entries, err := os.ReadDir(storageDir)
	if err != nil {
		t.Fatalf("read storage directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("oversized upload must not persist documents, got %d entries", len(entries))
	}
}

func TestUploadRejectsUnsupportedDocumentTypeWithoutPersistingDocument(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, storageDir, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})
	token := signUploadJWT(t, privPEM, jwt.MapClaims{
		"service_id": "test-service", "webhook_url": "https://test.example.com/callback",
		"file_name": "unsafe.bin", "document_type": "archive",
		"exp": time.Now().Add(time.Minute).Unix(),
	})
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("file", "unsafe.bin")
	_, _ = io.WriteString(part, "not a document")
	_ = form.Close()

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/documents", &body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", form.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unsupported document type, got %d", resp.StatusCode)
	}
	entries, err := os.ReadDir(storageDir)
	if err != nil {
		t.Fatalf("read storage directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("unsupported document type must not persist documents, got %d entries", len(entries))
	}
}

// S2: Upload with expired JWT returns 401 Unauthorized.
func TestUploadRejectsInvalidJWT(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	expiredJWT := signUploadJWT(t, privPEM, jwt.MapClaims{
		"service_id":  "test-service",
		"webhook_url": "https://test.example.com/callback",
		"file_name":   "test.docx",
		"exp":         time.Now().Add(-1 * time.Second).Unix(),
		"iat":         time.Now().Add(-120 * time.Second).Unix(),
	})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "test.docx")
	io.WriteString(part, "fake-content")
	writer.Close()

	req, _ := http.NewRequest("POST", server.URL+"/api/v1/documents", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+expiredJWT)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 401 Unauthorized for expired JWT, got %d\nbody: %s", resp.StatusCode, string(respBody))
	}
}

// S3: Upload with webhook domain not in whitelist returns 403.
func TestUploadRejectsUnauthorizedDomain(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"only-trusted.example.com"})

	validJWT := signUploadJWT(t, privPEM, jwt.MapClaims{
		"service_id":  "test-service",
		"webhook_url": "https://evil.example.com/exfiltrate",
		"file_name":   "test.docx",
		"exp":         time.Now().Add(60 * time.Second).Unix(),
		"iat":         time.Now().Unix(),
	})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "test.docx")
	io.WriteString(part, "fake-content")
	writer.Close()

	req, _ := http.NewRequest("POST", server.URL+"/api/v1/documents", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+validJWT)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 403 Forbidden for unauthorized webhook domain, got %d\nbody: %s", resp.StatusCode, string(respBody))
	}
}

// S4: Download while still editing returns 409 Conflict.
func TestDownloadReturns409WhileEditing(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/documents/"+docID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 409 Conflict while editing, got %d\nbody: %s", resp.StatusCode, string(respBody))
	}
}

func uploadTestDocument(t *testing.T, serverURL, privPEM, serviceID, webhookURL string) string {
	t.Helper()
	jwtToken := signUploadJWT(t, privPEM, jwt.MapClaims{
		"service_id":    serviceID,
		"webhook_url":   webhookURL,
		"file_name":     "test.docx",
		"document_type": "word",
		"exp":           time.Now().Add(60 * time.Second).Unix(),
		"iat":           time.Now().Unix(),
	})

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("file", "test.docx")
	io.WriteString(part, "test-content")
	w.Close()

	req, _ := http.NewRequest("POST", serverURL+"/api/v1/documents", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)
	docID, _ := result["document_id"].(string)
	if docID == "" {
		t.Fatalf("no document_id in response: %s", string(respBody))
	}
	return docID
}

// S5: Download returns edited document with 200.
func TestDownloadDocument(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")
	saveEditedViaCallback(t, server.URL, docID, "edited file content")

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/documents/"+docID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 OK, got %d\nbody: %s", resp.StatusCode, string(respBody))
	}

	downloaded, _ := io.ReadAll(resp.Body)
	if string(downloaded) != "edited file content" {
		t.Fatalf("expected 'edited file content', got '%s'", string(downloaded))
	}
}

func TestDocumentServerDownloadServesOriginalWithCacheHeaders(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")
	saveEditedViaCallback(t, server.URL, docID, "edited file content")

	resp, err := http.Get(server.URL + "/download/" + docID)
	if err != nil {
		t.Fatalf("request document server download: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "test-content" {
		t.Fatalf("expected document server download to serve original content, got %q", string(body))
	}
	if got := resp.Header.Get("Content-Length"); got != "12" {
		t.Fatalf("expected Content-Length 12, got %q", got)
	}
	if got := resp.Header.Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("expected byte range support, got Accept-Ranges %q", got)
	}
	if got := resp.Header.Get("ETag"); got == "" {
		t.Fatal("expected ETag header")
	}
	if got := resp.Header.Get("Cache-Control"); got != "private, max-age=28800" {
		t.Fatalf("expected document cache header, got %q", got)
	}
}

func TestOriginalDownloadFormatsUntrustedFilenameAsOneParameter(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})
	fileName := `report"; filename=attacker.txt`
	token := signUploadJWT(t, privPEM, jwt.MapClaims{
		"service_id": "test-service", "webhook_url": "https://test.example.com/callback",
		"file_name": fileName, "document_type": "word",
		"exp": time.Now().Add(time.Minute).Unix(),
	})
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("file", "report.docx")
	_, _ = io.WriteString(part, "document")
	_ = form.Close()
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/documents", &body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", form.FormDataContentType())
	uploadResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer uploadResp.Body.Close()
	var uploaded struct {
		DocumentID string `json:"document_id"`
	}
	_ = json.NewDecoder(uploadResp.Body).Decode(&uploaded)

	downloadResp, err := http.Get(server.URL + "/download/" + uploaded.DocumentID)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer downloadResp.Body.Close()
	disposition, params, err := mime.ParseMediaType(downloadResp.Header.Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("content disposition must be parseable: %v", err)
	}
	if disposition != "inline" || params["filename"] != fileName {
		t.Fatalf("expected one inline filename parameter %q, got %q %v", fileName, disposition, params)
	}
}

func TestDocumentServerDownloadSupportsByteRanges(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")

	req, _ := http.NewRequest("GET", server.URL+"/download/"+docID, nil)
	req.Header.Set("Range", "bytes=0-3")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request range download: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("expected 206 Partial Content, got %d: %s", resp.StatusCode, string(body))
	}
	if string(body) != "test" {
		t.Fatalf("expected first four bytes, got %q", string(body))
	}
	if got := resp.Header.Get("Content-Range"); got != "bytes 0-3/12" {
		t.Fatalf("expected Content-Range bytes 0-3/12, got %q", got)
	}
}

func TestDocumentServerDownloadRejectsInvalidByteRange(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")

	req, _ := http.NewRequest("GET", server.URL+"/download/"+docID, nil)
	req.Header.Set("Range", "bytes=100-200")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request invalid range download: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("expected 416, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Range"); got != "bytes */12" {
		t.Fatalf("expected Content-Range bytes */12, got %q", got)
	}
}

// S6: Download nonexistent document returns 404.
func TestDownloadReturns404ForMissing(t *testing.T) {
	_, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, "unused", pubPEM, []string{"test.example.com"})

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/documents/doc-nonexistent", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func saveEditedViaCallback(t *testing.T, serverURL, documentID, content string) {
	t.Helper()
	fakeDocServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(content))
	}))
	defer fakeDocServer.Close()

	callbackBody := bytes.NewReader(toJSON(map[string]interface{}{
		"status": 2,
		"key":    documentID,
		"url":    fakeDocServer.URL + "/cached-file.docx",
	}))
	req, _ := http.NewRequest("POST", callbackURL(serverURL, documentID), callbackBody)
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected callback 200, got %d", resp.StatusCode)
	}
	time.Sleep(500 * time.Millisecond)
}

// S7: ONLYOFFICE callback (status=2) saves the edited document.
func TestOOCallbackSavesDocument(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")

	editedContent := []byte("edited-by-onlyoffice")
	fakeDocServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(editedContent)
	}))
	defer fakeDocServer.Close()

	callbackBody := bytes.NewReader(toJSON(map[string]interface{}{
		"status": 2,
		"key":    docID,
		"url":    fakeDocServer.URL + "/cached-file.docx",
	}))
	req, _ := http.NewRequest("POST", callbackURL(server.URL, docID), callbackBody)
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected callback to return 200 OK, got %d", resp.StatusCode)
	}

	time.Sleep(500 * time.Millisecond)

	req2, _ := http.NewRequest("GET", server.URL+"/api/v1/documents/"+docID, nil)
	resp2, _ := http.DefaultClient.Do(req2)
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on download after callback, got %d", resp2.StatusCode)
	}

	downloaded, _ := io.ReadAll(resp2.Body)
	if string(downloaded) != "edited-by-onlyoffice" {
		t.Fatalf("expected 'edited-by-onlyoffice', got '%s'", string(downloaded))
	}
}

func TestOOCallbackDoesNotMarkEditedWhenDownloadFails(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})
	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")

	failingDocServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("temporary onlyoffice cache failure"))
	}))
	defer failingDocServer.Close()

	callbackReq, _ := http.NewRequest(http.MethodPost, callbackURL(server.URL, docID), bytes.NewReader(toJSON(map[string]interface{}{
		"status": 2,
		"key":    docID,
		"url":    failingDocServer.URL + "/cached-file.docx",
	})))
	callbackReq.Header.Set("Content-Type", "application/json")
	callbackResp, err := http.DefaultClient.Do(callbackReq)
	if err != nil {
		t.Fatalf("post callback: %v", err)
	}
	defer callbackResp.Body.Close()
	if callbackResp.StatusCode != http.StatusOK {
		t.Fatalf("expected callback 200, got %d", callbackResp.StatusCode)
	}

	time.Sleep(500 * time.Millisecond)

	downloadResp, err := http.Get(server.URL + "/api/v1/documents/" + docID)
	if err != nil {
		t.Fatalf("download latest document: %v", err)
	}
	defer downloadResp.Body.Close()
	if downloadResp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(downloadResp.Body)
		t.Fatalf("failed callback must leave document editing, got %d: %s", downloadResp.StatusCode, string(body))
	}

	metricsResp, err := http.Get(server.URL + "/api/v1/metrics")
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	defer metricsResp.Body.Close()
	metricsBody, _ := io.ReadAll(metricsResp.Body)
	if !strings.Contains(string(metricsBody), "onlyoffice_gateway_callback_save_failed_total 1") {
		t.Fatalf("expected failed save metric, got:\n%s", string(metricsBody))
	}
}

func TestMetricsUsePrometheusCounterMetadata(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/v1/metrics")
	if err != nil {
		t.Fatalf("get metrics: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	metrics := string(body)
	if !strings.Contains(metrics, "# HELP onlyoffice_gateway_callback_save_queued_total") ||
		!strings.Contains(metrics, "# TYPE onlyoffice_gateway_callback_save_queued_total counter") {
		t.Fatalf("expected Prometheus HELP and TYPE metadata, got:\n%s", metrics)
	}
}

// An unauthenticated callback must not make the gateway fetch an attacker URL.
func TestCallbackRejectsRequestWithoutCapabilityBeforeDownloading(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})
	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")

	downloaded := make(chan struct{}, 1)
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloaded <- struct{}{}
		w.Write([]byte("attacker-controlled-content"))
	}))
	defer attacker.Close()

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/callback", bytes.NewReader(toJSON(map[string]interface{}{
		"status": 2,
		"key":    docID,
		"url":    attacker.URL + "/file.docx",
	})))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post callback: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated callback to return 401, got %d", resp.StatusCode)
	}
	select {
	case <-downloaded:
		t.Fatal("unauthenticated callback must not download the supplied URL")
	case <-time.After(300 * time.Millisecond):
	}
}

func toJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

// S8: Callback debounce — rapid callbacks only process the latest.
func TestOOCallbackDebounce(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")

	fakeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("newest-version"))
	}))
	defer fakeServer.Close()

	for i := 0; i < 3; i++ {
		body := bytes.NewReader(toJSON(map[string]interface{}{
			"status": 2, "key": docID, "url": fakeServer.URL + "/file.docx",
		}))
		req, _ := http.NewRequest("POST", callbackURL(server.URL, docID), body)
		req.Header.Set("Content-Type", "application/json")
		http.DefaultClient.Do(req)
	}

	time.Sleep(500 * time.Millisecond)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/documents/"+docID, nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	content, _ := io.ReadAll(resp.Body)
	if string(content) != "newest-version" {
		t.Fatalf("expected 'newest-version', got '%s'", string(content))
	}
}

// S11: TTL extends when status=1 callback is received.
func TestTTLExtendOnEditing(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, storageDir, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")

	body := bytes.NewReader(toJSON(map[string]interface{}{
		"status": 1, "key": docID, "users": []string{"user-1"},
	}))
	req, _ := http.NewRequest("POST", callbackURL(server.URL, docID), body)
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	storageStore, err := storage.NewLocalStore(storageDir)
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	meta, err := storageStore.GetMeta(context.Background(), docID)
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}

	if time.Until(meta.ExpiresAt) < 55*time.Minute {
		t.Fatalf("expected TTL to be extended to ~1 hour from now, got %s", meta.ExpiresAt.Format(time.RFC3339))
	}
}

// S9: Webhook retries on failure, then gives up.
func TestWebhookRetriesThenGivesUp(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)

	attempts := 0
	webhookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer webhookServer.Close()

	whHost := strings.TrimPrefix(webhookServer.URL, "http://")
	whHost = whHost[:len(whHost)-len(":"+strings.Split(whHost, ":")[len(strings.Split(whHost, ":"))-1])]
	domain := "127.0.0.1"

	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com", domain})

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", webhookServer.URL+"/callback")

	editedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("edited"))
	}))
	defer editedServer.Close()

	body := bytes.NewReader(toJSON(map[string]interface{}{
		"status": 2, "key": docID, "url": editedServer.URL + "/file.docx",
	}))
	req, _ := http.NewRequest("POST", callbackURL(server.URL, docID), body)
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	time.Sleep(5 * time.Second)

	if attempts < 1 {
		t.Fatalf("expected at least 1 webhook attempt, got %d", attempts)
	}
	if attempts > 5 {
		t.Fatalf("expected at most 4 attempts (1 + 3 retries), got %d", attempts)
	}
}

// S10: Editor page returns valid HTML with api.js and placeholder.
func TestEditorPageReturnsHTML(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")

	editToken := signJWT(t, privPEM, jwt.MapClaims{
		"service_id":  "test-service",
		"document_id": docID,
		"exp":         time.Now().Add(30 * time.Minute).Unix(),
		"iat":         time.Now().Unix(),
	})
	req, _ := http.NewRequest("GET", server.URL+"/edit?token="+editToken, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("expected editor response to disable referrers, got %q", got)
	}

	html, _ := io.ReadAll(resp.Body)
	htmlStr := string(html)

	if !contains(htmlStr, "api.js") {
		t.Fatalf("expected editor page to contain api.js, got: %s", truncate(htmlStr, 300))
	}
	if !contains(htmlStr, "placeholder") {
		t.Fatalf("expected editor page to contain placeholder, got: %s", truncate(htmlStr, 300))
	}
	if !contains(htmlStr, "postMessage") {
		t.Fatalf("expected editor page to contain postMessage, got: %s", truncate(htmlStr, 300))
	}
}

func TestEditorEscapesUntrustedDocumentTitle(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})
	maliciousName := `</title><script>window.injected=true</script>`
	uploadToken := signUploadJWT(t, privPEM, jwt.MapClaims{
		"service_id": "test-service", "webhook_url": "https://test.example.com/callback",
		"file_name": maliciousName, "document_type": "word",
		"exp": time.Now().Add(time.Minute).Unix(),
	})
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("file", "document.docx")
	_, _ = io.WriteString(part, "document")
	_ = form.Close()
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/documents", &body)
	req.Header.Set("Authorization", "Bearer "+uploadToken)
	req.Header.Set("Content-Type", form.FormDataContentType())
	uploadResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer uploadResp.Body.Close()
	var uploaded struct {
		DocumentID string `json:"document_id"`
	}
	if err := json.NewDecoder(uploadResp.Body).Decode(&uploaded); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}

	editToken := signJWT(t, privPEM, jwt.MapClaims{
		"service_id": "test-service", "document_id": uploaded.DocumentID,
		"exp": time.Now().Add(time.Minute).Unix(),
	})
	editResp, err := http.Get(server.URL + "/edit?token=" + editToken)
	if err != nil {
		t.Fatalf("open editor: %v", err)
	}
	defer editResp.Body.Close()
	html, _ := io.ReadAll(editResp.Body)
	if strings.Contains(string(html), maliciousName) {
		t.Fatalf("editor page rendered unescaped document title: %s", maliciousName)
	}
}

// S12: Expired documents are cleaned up.
func TestExpiredDocumentCleaned(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)

	tmpDir := t.TempDir()
	storageDir := filepath.Join(tmpDir, "storage")

	cfg := &config.Config{
		ListenAddr:        "127.0.0.1:18080",
		DocumentServerURL: "https://doc.example.com",
		JWTSecret:         "test-gateway-jwt-secret",
		StorageDir:        storageDir,
		TTLHours:          -1,
		WebhookMaxRetries: 3,
	}
	loaded, _ := config.FromLiteral(cfg)

	store := admin.NewInMemoryServiceStore()
	store.Add(admin.ServiceRecord{
		ID:                    "test-service",
		PublicKeyPEM:          pubPEM,
		AllowedWebhookDomains: []string{"test.example.com"},
	})

	handler := gateway.NewHandler(loaded, store)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")

	editToken := signJWT(t, privPEM, jwt.MapClaims{
		"service_id":  "test-service",
		"document_id": docID,
		"exp":         time.Now().Add(30 * time.Minute).Unix(),
		"iat":         time.Now().Unix(),
	})
	req, _ := http.NewRequest("GET", server.URL+"/edit?token="+editToken, nil)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected document to exist before cleanup, got %d", resp.StatusCode)
	}

	storageStore, _ := storage.NewLocalStore(storageDir)
	count, err := storageStore.Expire(context.Background())
	if err != nil {
		t.Fatalf("expire failed: %v", err)
	}
	if count < 1 {
		t.Fatal("expected at least 1 expired document to be cleaned")
	}

	req2, _ := http.NewRequest("GET", server.URL+"/edit?token="+editToken, nil)
	resp2, _ := http.DefaultClient.Do(req2)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after cleanup, got %d", resp2.StatusCode)
	}
}

func TestExpiredDocumentsAreCleanedAutomatically(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)

	tmpDir := t.TempDir()
	storageDir := filepath.Join(tmpDir, "storage")
	cfg := &config.Config{
		ListenAddr:        "127.0.0.1:18080",
		DocumentServerURL: "https://doc.example.com",
		JWTSecret:         "test-gateway-jwt-secret",
		StorageDir:        storageDir,
		TTLHours:          -1,
		CleanupInterval:   20 * time.Millisecond,
		WebhookMaxRetries: 3,
	}
	loaded, _ := config.FromLiteral(cfg)

	store := admin.NewInMemoryServiceStore()
	store.Add(admin.ServiceRecord{
		ID:                    "test-service",
		PublicKeyPEM:          pubPEM,
		AllowedWebhookDomains: []string{"test.example.com"},
	})

	server := httptest.NewServer(gateway.NewHandler(loaded, store))
	t.Cleanup(server.Close)

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")
	editToken := signJWT(t, privPEM, jwt.MapClaims{
		"service_id":  "test-service",
		"document_id": docID,
		"exp":         time.Now().Add(30 * time.Minute).Unix(),
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/edit?token="+editToken, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("open editor: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected automatic cleanup to remove expired document, last status %d", resp.StatusCode)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// S24: Callback debounce — rapid callbacks within 200ms only process the last.
func TestCallbackDebounceSkipsWithinWindow(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")

	var versionLock sync.Mutex
	currentVersion := "version-1"
	fakeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		versionLock.Lock()
		v := currentVersion
		versionLock.Unlock()
		w.Write([]byte(v))
	}))
	defer fakeServer.Close()

	for i := 1; i <= 5; i++ {
		versionLock.Lock()
		currentVersion = fmt.Sprintf("version-%d", i)
		versionLock.Unlock()
		body := bytes.NewReader(toJSON(map[string]interface{}{
			"status": 2, "key": docID, "url": fakeServer.URL + "/file.docx",
		}))
		req, _ := http.NewRequest("POST", callbackURL(server.URL, docID), body)
		req.Header.Set("Content-Type", "application/json")
		http.DefaultClient.Do(req)
	}

	time.Sleep(600 * time.Millisecond)

	resp, err := http.Get(server.URL + "/api/v1/documents/" + docID)
	if err != nil {
		t.Fatalf("download edited file: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected edited download 200, got %d: %s", resp.StatusCode, string(data))
	}
	if string(data) != "version-5" {
		t.Fatalf("expected version-5 from last (debounced) callback, got: %s", string(data))
	}
}

// S25: Webhook POST includes X-Gateway-Signature HMAC header.
func TestWebhookIncludesSignature(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)

	var receivedSig string
	var receivedBody string
	webhookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSig = r.Header.Get("X-Gateway-Signature")
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer webhookServer.Close()

	domain := "127.0.0.1"
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com", domain})

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", webhookServer.URL+"/callback")

	editServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hmac-test-content"))
	}))
	defer editServer.Close()

	body := bytes.NewReader(toJSON(map[string]interface{}{
		"status": 2, "key": docID, "url": editServer.URL + "/file.docx",
	}))
	req, _ := http.NewRequest("POST", callbackURL(server.URL, docID), body)
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	time.Sleep(500 * time.Millisecond)

	if receivedSig == "" {
		t.Fatal("expected X-Gateway-Signature header in webhook, got none")
	}
	if receivedBody == "" {
		t.Fatal("expected webhook body")
	}
	if !strings.Contains(receivedBody, "document.saved") {
		t.Fatalf("expected webhook body to contain document.saved, got: %s", receivedBody)
	}
}

// S26: /edit without valid token returns 401.
func TestEditRequiresValidToken(t *testing.T) {
	_, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, "unused", pubPEM, []string{"test.example.com"})

	req, _ := http.NewRequest("GET", server.URL+"/edit?document_id=doc-123", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing token, got %d", resp.StatusCode)
	}
}

func TestEditAcceptsValidToken(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")

	editToken := signJWT(t, privPEM, jwt.MapClaims{
		"service_id":  "test-service",
		"document_id": docID,
		"exp":         time.Now().Add(30 * time.Minute).Unix(),
		"iat":         time.Now().Unix(),
	})

	req, _ := http.NewRequest("GET", server.URL+"/edit?token="+editToken, nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 with valid token, got %d: %s", resp.StatusCode, string(respBody))
	}

	html, _ := io.ReadAll(resp.Body)
	if !contains(string(html), "api.js") {
		t.Fatal("expected editor page to contain api.js")
	}
}

func TestEditViewModeRendersReadonlyConfig(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")

	editToken := signJWT(t, privPEM, jwt.MapClaims{
		"service_id":  "test-service",
		"document_id": docID,
		"exp":         time.Now().Add(30 * time.Minute).Unix(),
		"iat":         time.Now().Unix(),
	})

	req, _ := http.NewRequest("GET", server.URL+"/edit?token="+editToken+"&mode=view", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	html, _ := io.ReadAll(resp.Body)
	htmlStr := string(html)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(htmlStr, 200))
	}

	editorConfig := extractEditorConfig(t, htmlStr)
	ec := editorConfig["editorConfig"].(map[string]interface{})
	if ec["mode"] != "view" {
		t.Fatalf("expected editorConfig to include view mode, got: %v", ec["mode"])
	}
	if _, ok := editorConfig["mode"]; ok {
		t.Fatalf("expected view mode under editorConfig, got top-level mode %v", editorConfig["mode"])
	}
	permissions := editorConfig["document"].(map[string]interface{})["permissions"].(map[string]interface{})
	if permissions["edit"] != false {
		t.Fatalf("expected editor config to disable editing, got: %v", permissions["edit"])
	}
	if permissions["download"] != true {
		t.Fatalf("expected editor config to allow downloads, got: %v", permissions["download"])
	}
}

func TestEditUsesForwardedPublicURLForDocumentServerCallbacks(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")

	editToken := signJWT(t, privPEM, jwt.MapClaims{
		"service_id":  "test-service",
		"document_id": docID,
		"exp":         time.Now().Add(30 * time.Minute).Unix(),
		"iat":         time.Now().Unix(),
	})

	req, _ := http.NewRequest("GET", server.URL+"/edit?token="+editToken, nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "doc-gateway.codeshell.cc")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	html, _ := io.ReadAll(resp.Body)
	htmlStr := string(html)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(htmlStr, 200))
	}
	if !contains(htmlStr, `"url":"https://doc-gateway.codeshell.cc/download/`+docID+`"`) {
		t.Fatalf("expected forwarded https download url, got: %s", truncate(htmlStr, 800))
	}
	if !contains(htmlStr, `"callbackUrl":"https://doc-gateway.codeshell.cc/callback?token=`) {
		t.Fatalf("expected forwarded https callback url, got: %s", truncate(htmlStr, 800))
	}
	if contains(htmlStr, `http://doc-gateway.codeshell.cc/download/`) {
		t.Fatalf("expected no downgraded http download url, got: %s", truncate(htmlStr, 800))
	}
}

func TestEditIncludesSignedOnlyOfficeConfigToken(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")

	editToken := signJWT(t, privPEM, jwt.MapClaims{
		"service_id":  "test-service",
		"document_id": docID,
		"exp":         time.Now().Add(30 * time.Minute).Unix(),
		"iat":         time.Now().Unix(),
	})

	req, _ := http.NewRequest("GET", server.URL+"/edit?token="+editToken, nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	html, _ := io.ReadAll(resp.Body)
	htmlStr := string(html)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(htmlStr, 200))
	}
	if !contains(htmlStr, `"token":"`) {
		t.Fatalf("expected signed ONLYOFFICE config token in editor HTML, got: %s", truncate(htmlStr, 800))
	}
}

func TestEditUsesPublicDocumentServerURLForBrowserScript(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	tmpDir := t.TempDir()
	cfg := &config.Config{
		ListenAddr:              "127.0.0.1:18080",
		DocumentServerURL:       "http://document-server",
		DocumentServerPublicURL: "https://office.example.com",
		JWTSecret:               "test-gateway-jwt-secret",
		StorageDir:              filepath.Join(tmpDir, "storage"),
		TTLHours:                8,
		WebhookMaxRetries:       3,
	}
	loaded, _ := config.FromLiteral(cfg)

	store := admin.NewInMemoryServiceStore()
	store.Add(admin.ServiceRecord{
		ID:                    "test-service",
		PublicKeyPEM:          pubPEM,
		AllowedWebhookDomains: []string{"test.example.com"},
	})

	handler := gateway.NewHandler(loaded, store)
	server := httptest.NewServer(handler)
	defer server.Close()

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")
	editToken := signJWT(t, privPEM, jwt.MapClaims{
		"service_id":  "test-service",
		"document_id": docID,
		"exp":         time.Now().Add(30 * time.Minute).Unix(),
		"iat":         time.Now().Unix(),
	})

	req, _ := http.NewRequest("GET", server.URL+"/edit?token="+editToken, nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	html, _ := io.ReadAll(resp.Body)
	htmlStr := string(html)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(htmlStr, 200))
	}
	if !contains(htmlStr, `<script src="https://office.example.com/web-apps/apps/api/documents/api.js"></script>`) {
		t.Fatalf("expected public document server script URL, got: %s", truncate(htmlStr, 800))
	}
	if contains(htmlStr, `<script src="http://document-server/`) {
		t.Fatalf("expected no internal document server script URL, got: %s", truncate(htmlStr, 800))
	}
}

func TestEditDefaultsBrowserScriptToForwardedGatewayURL(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")
	editToken := signJWT(t, privPEM, jwt.MapClaims{
		"service_id":  "test-service",
		"document_id": docID,
		"exp":         time.Now().Add(30 * time.Minute).Unix(),
		"iat":         time.Now().Unix(),
	})

	req, _ := http.NewRequest("GET", server.URL+"/edit?token="+editToken, nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "doc-gateway.codeshell.cc")
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()

	html, _ := io.ReadAll(resp.Body)
	htmlStr := string(html)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, truncate(htmlStr, 200))
	}
	if !contains(htmlStr, `<script src="https://doc-gateway.codeshell.cc/web-apps/apps/api/documents/api.js"></script>`) {
		t.Fatalf("expected forwarded gateway script URL, got: %s", truncate(htmlStr, 800))
	}
	if contains(htmlStr, `<script src="http://document-server/`) {
		t.Fatalf("expected no internal document server script URL, got: %s", truncate(htmlStr, 800))
	}
}

func TestGatewayProxiesDocumentServerWebAssets(t *testing.T) {
	_, pubPEM := generateRSAKeyPair(t)
	var proxiedPath string
	documentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxiedPath = r.URL.Path
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "application/javascript")
		w.Write([]byte("window.DocsAPI = {};"))
	}))
	defer documentServer.Close()

	cfg := &config.Config{
		ListenAddr:        "127.0.0.1:18080",
		DocumentServerURL: documentServer.URL,
		JWTSecret:         "test-gateway-jwt-secret",
		StorageDir:        filepath.Join(t.TempDir(), "storage"),
		TTLHours:          8,
		WebhookMaxRetries: 3,
	}
	loaded, _ := config.FromLiteral(cfg)

	store := admin.NewInMemoryServiceStore()
	store.Add(admin.ServiceRecord{
		ID:                    "test-service",
		PublicKeyPEM:          pubPEM,
		AllowedWebhookDomains: []string{"test.example.com"},
	})

	server := httptest.NewServer(gateway.NewHandler(loaded, store))
	defer server.Close()

	resp, err := http.Get(server.URL + "/web-apps/apps/api/documents/api.js")
	if err != nil {
		t.Fatalf("request proxied asset: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from proxied document server asset, got %d", resp.StatusCode)
	}
	if proxiedPath != "/web-apps/apps/api/documents/api.js" {
		t.Fatalf("expected proxied path, got %q", proxiedPath)
	}
	if string(body) != "window.DocsAPI = {};" {
		t.Fatalf("expected proxied body, got %q", string(body))
	}
	if got, want := resp.Header.Get("Cache-Control"), "public, max-age=300, stale-while-revalidate=86400"; got != want {
		t.Fatalf("expected api.js cache header %q, got %q", want, got)
	}
}

func TestGatewayProxiesVersionedDocumentServerWebAssets(t *testing.T) {
	_, pubPEM := generateRSAKeyPair(t)
	var proxiedPath string
	documentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxiedPath = r.URL.Path
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>document editor</html>"))
	}))
	defer documentServer.Close()

	cfg := &config.Config{
		ListenAddr:        "127.0.0.1:18080",
		DocumentServerURL: documentServer.URL,
		JWTSecret:         "test-gateway-jwt-secret",
		StorageDir:        filepath.Join(t.TempDir(), "storage"),
		TTLHours:          8,
		WebhookMaxRetries: 3,
	}
	loaded, _ := config.FromLiteral(cfg)

	store := admin.NewInMemoryServiceStore()
	store.Add(admin.ServiceRecord{
		ID:                    "test-service",
		PublicKeyPEM:          pubPEM,
		AllowedWebhookDomains: []string{"test.example.com"},
	})

	server := httptest.NewServer(gateway.NewHandler(loaded, store))
	defer server.Close()

	resp, err := http.Get(server.URL + "/6.4.2-6/web-apps/apps/documenteditor/main/index.html?_dc=6.4.2-6")
	if err != nil {
		t.Fatalf("request proxied versioned asset: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from proxied versioned document server asset, got %d", resp.StatusCode)
	}
	if proxiedPath != "/6.4.2-6/web-apps/apps/documenteditor/main/index.html" {
		t.Fatalf("expected proxied versioned path, got %q", proxiedPath)
	}
	if string(body) != "<html>document editor</html>" {
		t.Fatalf("expected proxied body, got %q", string(body))
	}
	if got, want := resp.Header.Get("Cache-Control"), "public, max-age=31536000, immutable"; got != want {
		t.Fatalf("expected versioned asset cache header %q, got %q", want, got)
	}
}

func TestGatewayCachesDocumentServerCacheAssets(t *testing.T) {
	_, pubPEM := generateRSAKeyPair(t)
	documentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "application/javascript")
		w.Write([]byte("cached asset"))
	}))
	defer documentServer.Close()

	cfg := &config.Config{
		ListenAddr:        "127.0.0.1:18080",
		DocumentServerURL: documentServer.URL,
		JWTSecret:         "test-gateway-jwt-secret",
		StorageDir:        filepath.Join(t.TempDir(), "storage"),
		TTLHours:          8,
		WebhookMaxRetries: 3,
	}
	loaded, _ := config.FromLiteral(cfg)

	store := admin.NewInMemoryServiceStore()
	store.Add(admin.ServiceRecord{
		ID:                    "test-service",
		PublicKeyPEM:          pubPEM,
		AllowedWebhookDomains: []string{"test.example.com"},
	})

	server := httptest.NewServer(gateway.NewHandler(loaded, store))
	defer server.Close()

	resp, err := http.Get(server.URL + "/cache/files/main.js")
	if err != nil {
		t.Fatalf("request cached document server asset: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from proxied cache asset, got %d", resp.StatusCode)
	}
	if got, want := resp.Header.Get("Cache-Control"), "public, max-age=86400"; got != want {
		t.Fatalf("expected cache asset header %q, got %q", want, got)
	}
}

func TestGatewayDoesNotProxyNonVersionedUnknownRootPaths(t *testing.T) {
	_, pubPEM := generateRSAKeyPair(t)
	proxyHit := false
	documentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer documentServer.Close()

	cfg := &config.Config{
		ListenAddr:        "127.0.0.1:18080",
		DocumentServerURL: documentServer.URL,
		JWTSecret:         "test-gateway-jwt-secret",
		StorageDir:        filepath.Join(t.TempDir(), "storage"),
		TTLHours:          8,
		WebhookMaxRetries: 3,
	}
	loaded, _ := config.FromLiteral(cfg)

	store := admin.NewInMemoryServiceStore()
	store.Add(admin.ServiceRecord{
		ID:                    "test-service",
		PublicKeyPEM:          pubPEM,
		AllowedWebhookDomains: []string{"test.example.com"},
	})

	server := httptest.NewServer(gateway.NewHandler(loaded, store))
	defer server.Close()

	resp, err := http.Get(server.URL + "/not-a-document-server-path")
	if err != nil {
		t.Fatalf("request unknown root path: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown root path, got %d", resp.StatusCode)
	}
	if proxyHit {
		t.Fatal("expected unknown root path not to be proxied to document server")
	}
}

func TestGatewayDoesNotOverrideDynamicDocumentServerCacheHeaders(t *testing.T) {
	_, pubPEM := generateRSAKeyPair(t)
	documentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte("dynamic document server response"))
	}))
	defer documentServer.Close()

	cfg := &config.Config{
		ListenAddr:        "127.0.0.1:18080",
		DocumentServerURL: documentServer.URL,
		JWTSecret:         "test-gateway-jwt-secret",
		StorageDir:        filepath.Join(t.TempDir(), "storage"),
		TTLHours:          8,
		WebhookMaxRetries: 3,
	}
	loaded, _ := config.FromLiteral(cfg)

	store := admin.NewInMemoryServiceStore()
	store.Add(admin.ServiceRecord{
		ID:                    "test-service",
		PublicKeyPEM:          pubPEM,
		AllowedWebhookDomains: []string{"test.example.com"},
	})

	server := httptest.NewServer(gateway.NewHandler(loaded, store))
	defer server.Close()

	resp, err := http.Get(server.URL + "/doc/editor")
	if err != nil {
		t.Fatalf("request dynamic document server path: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("expected dynamic cache header to pass through, got %q", got)
	}
}

func signJWT(t *testing.T, privateKeyPEM string, claims jwt.MapClaims) string {
	t.Helper()
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		t.Fatal("failed to decode private key PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return signed
}

// S28: /edit page renders branding from upload JWT via config builder.
func TestEditRendersBrandingFromUpload(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, _, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	brandedJWT := signJWT(t, privPEM, jwt.MapClaims{
		"service_id":    "test-service",
		"webhook_url":   "https://test.example.com/callback",
		"file_name":     "branded.docx",
		"document_type": "word",
		"branding": map[string]interface{}{
			"logo_url":    "https://test.example.com/logo.png",
			"language":    "zh-CN",
			"color_theme": "#ff6600",
		},
		"exp": time.Now().Add(60 * time.Second).Unix(),
		"iat": time.Now().Unix(),
	})

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("file", "branded.docx")
	io.WriteString(part, "branded-content")
	w.Close()

	req, _ := http.NewRequest("POST", server.URL+"/api/v1/documents", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+brandedJWT)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	docID, _ := result["document_id"].(string)

	if docID == "" {
		t.Fatal("no document_id in upload response")
	}

	editToken := signJWT(t, privPEM, jwt.MapClaims{
		"service_id":  "test-service",
		"document_id": docID,
		"exp":         time.Now().Add(30 * time.Minute).Unix(),
		"iat":         time.Now().Unix(),
	})

	req2, _ := http.NewRequest("GET", server.URL+"/edit?token="+editToken, nil)
	resp2, _ := http.DefaultClient.Do(req2)
	defer resp2.Body.Close()

	html, _ := io.ReadAll(resp2.Body)
	htmlStr := string(html)

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp2.StatusCode, htmlStr[:min(len(htmlStr), 200)])
	}

	if !contains(htmlStr, "https://test.example.com/logo.png") {
		t.Fatalf("expected logo_url in editor HTML, got: %s", truncate(htmlStr, 400))
	}
	if !contains(htmlStr, "zh-CN") {
		t.Fatalf("expected language zh-CN in editor HTML: %s", truncate(htmlStr, 400))
	}
}

// S29: Gateway can report Document Server connectivity status.
func TestDocumentServerHealthCheck(t *testing.T) {
	fakeDS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer fakeDS.Close()

	_, pubPEM := generateRSAKeyPair(t)
	tmpDir := t.TempDir()

	cfg := &config.Config{
		ListenAddr:        "127.0.0.1:18080",
		DocumentServerURL: fakeDS.URL,
		JWTSecret:         "test-secret",
		StorageDir:        filepath.Join(tmpDir, "storage"),
		TTLHours:          8,
		WebhookMaxRetries: 3,
	}
	loaded, _ := config.FromLiteral(cfg)

	store := admin.NewInMemoryServiceStore()
	store.Add(admin.ServiceRecord{
		ID:                    "test",
		PublicKeyPEM:          pubPEM,
		AllowedWebhookDomains: []string{"localhost"},
	})

	handler := gateway.NewHandler(loaded, store)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	req, _ := http.NewRequest("GET", server.URL+"/api/v1/health/ds", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	ok, _ := result["document_server_ok"].(bool)
	if !ok {
		t.Fatalf("expected document_server_ok=true, got: %v", result)
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func extractEditorConfig(t *testing.T, html string) map[string]interface{} {
	t.Helper()

	start := strings.Index(html, "var config = ")
	if start == -1 {
		t.Fatal("expected editor page to declare config")
	}
	start += len("var config = ")

	end := strings.Index(html[start:], ";\n\n  function post")
	if end == -1 {
		t.Fatal("expected editor config declaration to terminate before post helper")
	}

	var config map[string]interface{}
	if err := json.Unmarshal([]byte(html[start:start+end]), &config); err != nil {
		t.Fatalf("unmarshal editor config: %v", err)
	}
	return config
}
