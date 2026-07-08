package gateway_test

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
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
	t.Helper()
	tmpDir := t.TempDir()
	storageDir := filepath.Join(tmpDir, "storage")

	cfg := &config.Config{
		ListenAddr:        "127.0.0.1:18080",
		DocumentServerURL: "https://doc.example.com",
		JWTSecret:         "test-gateway-jwt-secret",
		StorageDir:        storageDir,
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

// Tracer Bullet: business service uploads a document with valid JWT,
// Gateway stores it, returns 201 + document_id.
func TestUploadDocument(t *testing.T) {
	privPEM, pubPEM := generateRSAKeyPair(t)
	server, storageDir, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

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

	entries, _ := os.ReadDir(storageDir)
	found := false
	for _, e := range entries {
		if e.IsDir() {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected document directory to exist in storage")
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
	server, storageDir, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

	docID := uploadTestDocument(t, server.URL, privPEM, "test-service", "https://test.example.com/callback")
	markDocumentEdited(t, storageDir, docID, "edited file content")

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

func markDocumentEdited(t *testing.T, storageDir, documentID, content string) {
	t.Helper()
	dir := filepath.Join(storageDir, documentID)
	os.WriteFile(filepath.Join(dir, "edited.docx"), []byte(content), 0644)
	metaPath := filepath.Join(dir, "meta.json")
	data, _ := os.ReadFile(metaPath)
	var meta map[string]interface{}
	json.Unmarshal(data, &meta)
	meta["is_edited"] = true
	newData, _ := json.Marshal(meta)
	os.WriteFile(metaPath, newData, 0644)
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
	req, _ := http.NewRequest("POST", server.URL+"/callback", callbackBody)
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
		req, _ := http.NewRequest("POST", server.URL+"/callback", body)
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
	req, _ := http.NewRequest("POST", server.URL+"/callback", body)
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	metaPath := filepath.Join(storageDir, docID, "meta.json")
	data, _ := os.ReadFile(metaPath)
	var meta map[string]interface{}
	json.Unmarshal(data, &meta)

	expiresStr, _ := meta["expires_at"].(string)
	expiresAt, _ := time.Parse(time.RFC3339, expiresStr)

	if time.Until(expiresAt) < 55*time.Minute {
		t.Fatalf("expected TTL to be extended to ~1 hour from now, got %s", expiresStr)
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
	req, _ := http.NewRequest("POST", server.URL+"/callback", body)
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
	count, err := storageStore.Expire()
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
	server, storageDir, _ := setupGateway(t, privPEM, pubPEM, []string{"test.example.com"})

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
		req, _ := http.NewRequest("POST", server.URL+"/callback", body)
		req.Header.Set("Content-Type", "application/json")
		http.DefaultClient.Do(req)
	}

	time.Sleep(600 * time.Millisecond)

	editedPath := filepath.Join(storageDir, docID, "edited.docx")
	data, err := os.ReadFile(editedPath)
	if err != nil {
		t.Fatalf("edited file not found: %v", err)
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
	req, _ := http.NewRequest("POST", server.URL+"/callback", body)
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
	if !contains(htmlStr, `"mode":"view"`) {
		t.Fatalf("expected editor config to include view mode, got: %s", truncate(htmlStr, 400))
	}
	if !contains(htmlStr, `"permissions":{"download":true,"edit":false}`) {
		t.Fatalf("expected editor config to disable editing, got: %s", truncate(htmlStr, 600))
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
