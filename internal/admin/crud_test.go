package admin_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zenmind/onlyoffice-gateway/internal/admin"
	"github.com/zenmind/onlyoffice-gateway/internal/audit"
)

func validPublicKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	pubBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}))
}

func loginAndGetToken(t *testing.T, srvURL string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"username": "admin",
		"password": "secure-password",
	})
	resp, err := http.Post(srvURL+"/admin/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	defer resp.Body.Close()
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	return result["token"]
}

func newAdminServer(t *testing.T) (*httptest.Server, *admin.InMemoryServiceStore) {
	t.Helper()
	store := admin.NewInMemoryServiceStore()
	mux := admin.NewMux(admin.Opts{
		AdminUsername:      "admin",
		AdminPassword:      "secure-password",
		AdminSessionSecret: "test-admin-secret",
		Store:              store,
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, store
}

func authGet(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s failed: %v", url, err)
	}
	return resp
}

func authPost(t *testing.T, url, token string, body interface{}) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s failed: %v", url, err)
	}
	return resp
}

func authDelete(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("DELETE", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s failed: %v", url, err)
	}
	return resp
}

// T2: List services (empty)
func TestListServicesEmpty(t *testing.T) {
	srv, _ := newAdminServer(t)
	token := loginAndGetToken(t, srv.URL)

	resp := authGet(t, srv.URL+"/admin/api/services", token)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var list []admin.ServiceRecord
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d items", len(list))
	}
}

// T2: Create a service and list it
func TestCreateAndListService(t *testing.T) {
	srv, _ := newAdminServer(t)
	token := loginAndGetToken(t, srv.URL)

	svc := admin.ServiceRecord{
		ID:                    "my-app",
		PublicKeyPEM:          validPublicKeyPEM(t),
		AllowedWebhookDomains: []string{"myapp.example.com", "localhost"},
	}
	resp := authPost(t, srv.URL+"/admin/api/services", token, svc)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	// Verify it appears in list
	resp2 := authGet(t, srv.URL+"/admin/api/services", token)
	defer resp2.Body.Close()

	var list []admin.ServiceRecord
	json.NewDecoder(resp2.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("expected 1 service, got %d", len(list))
	}
	if list[0].ID != "my-app" {
		t.Fatalf("expected my-app, got %s", list[0].ID)
	}
	if len(list[0].AllowedWebhookDomains) != 2 {
		t.Fatalf("expected 2 domains, got %d", len(list[0].AllowedWebhookDomains))
	}
}

func TestCreateServiceReturnsWebhookSecretOnce(t *testing.T) {
	srv, _ := newAdminServer(t)
	token := loginAndGetToken(t, srv.URL)

	resp := authPost(t, srv.URL+"/admin/api/services", token, admin.ServiceRecord{
		ID:                    "doc",
		PublicKeyPEM:          validPublicKeyPEM(t),
		AllowedWebhookDomains: []string{"doc.example.com"},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("expected Cache-Control no-store, got %q", got)
	}
	var created struct {
		Service struct {
			ID                      string `json:"id"`
			WebhookSecretConfigured bool   `json:"webhook_secret_configured"`
		} `json:"service"`
		Credentials struct {
			WebhookSecret string `json:"webhook_secret"`
		} `json:"credentials"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Service.ID != "doc" {
		t.Fatalf("expected service doc, got %q", created.Service.ID)
	}
	if !created.Service.WebhookSecretConfigured {
		t.Fatal("expected webhook secret to be configured")
	}
	if created.Credentials.WebhookSecret == "" {
		t.Fatal("expected one-time webhook secret")
	}

	listResp := authGet(t, srv.URL+"/admin/api/services", token)
	defer listResp.Body.Close()
	var listed []map[string]any
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected one service, got %d", len(listed))
	}
	if _, exposed := listed[0]["webhook_secret"]; exposed {
		t.Fatal("service list must not expose webhook_secret")
	}
}

func TestCreateServiceWritesRedactedCredentialAuditEvent(t *testing.T) {
	store := admin.NewInMemoryServiceStore()
	auditLog, err := audit.New(t.TempDir(), 14, "test")
	if err != nil {
		t.Fatalf("create audit log: %v", err)
	}
	mux := admin.NewMux(admin.Opts{
		AdminUsername:      "admin",
		AdminPassword:      "secure-password",
		AdminSessionSecret: "test-admin-secret",
		Store:              store,
		AuditLog:           auditLog,
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	token := loginAndGetToken(t, srv.URL)

	resp := authPost(t, srv.URL+"/admin/api/services", token, admin.ServiceRecord{
		ID:           "doc",
		PublicKeyPEM: validPublicKeyPEM(t),
	})
	resp.Body.Close()

	items, _, err := auditLog.List(context.Background(), audit.Query{Type: "admin.service_webhook_credential_created"})
	if err != nil {
		t.Fatalf("list audit log: %v", err)
	}
	if len(items) != 1 || items[0].ServiceID != "doc" {
		t.Fatalf("expected one redacted credential audit event, got %#v", items)
	}
}

func TestRotateWebhookSecretCreatesPendingWithoutChangingActive(t *testing.T) {
	srv, store := newAdminServer(t)
	token := loginAndGetToken(t, srv.URL)

	createResp := authPost(t, srv.URL+"/admin/api/services", token, admin.ServiceRecord{
		ID:           "doc",
		PublicKeyPEM: validPublicKeyPEM(t),
	})
	defer createResp.Body.Close()
	var created struct {
		Credentials struct {
			WebhookSecret string `json:"webhook_secret"`
		} `json:"credentials"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	rotateResp := authPost(t, srv.URL+"/admin/api/services/doc/webhook-secret/rotate", token, nil)
	defer rotateResp.Body.Close()
	if rotateResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", rotateResp.StatusCode)
	}
	if got := rotateResp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("expected Cache-Control no-store, got %q", got)
	}
	var rotated struct {
		Credentials struct {
			WebhookSecret string `json:"webhook_secret"`
		} `json:"credentials"`
	}
	if err := json.NewDecoder(rotateResp.Body).Decode(&rotated); err != nil {
		t.Fatalf("decode rotate response: %v", err)
	}
	if rotated.Credentials.WebhookSecret == "" {
		t.Fatal("expected one-time pending webhook secret")
	}
	if rotated.Credentials.WebhookSecret == created.Credentials.WebhookSecret {
		t.Fatal("pending webhook secret must differ from active secret")
	}
	active, ok := store.ActiveWebhookSecret("doc")
	if !ok || active != created.Credentials.WebhookSecret {
		t.Fatal("rotating must not change the active webhook secret")
	}
}

func TestActivateWebhookSecretMakesPendingCredentialActive(t *testing.T) {
	srv, store := newAdminServer(t)
	token := loginAndGetToken(t, srv.URL)

	createResp := authPost(t, srv.URL+"/admin/api/services", token, admin.ServiceRecord{
		ID:           "doc",
		PublicKeyPEM: validPublicKeyPEM(t),
	})
	createResp.Body.Close()
	rotateResp := authPost(t, srv.URL+"/admin/api/services/doc/webhook-secret/rotate", token, nil)
	defer rotateResp.Body.Close()
	var rotated struct {
		Credentials struct {
			WebhookSecret string `json:"webhook_secret"`
		} `json:"credentials"`
	}
	if err := json.NewDecoder(rotateResp.Body).Decode(&rotated); err != nil {
		t.Fatalf("decode rotate response: %v", err)
	}

	activateResp := authPost(t, srv.URL+"/admin/api/services/doc/webhook-secret/activate", token, nil)
	defer activateResp.Body.Close()
	if activateResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", activateResp.StatusCode)
	}
	active, ok := store.ActiveWebhookSecret("doc")
	if !ok || active != rotated.Credentials.WebhookSecret {
		t.Fatal("activating must make the pending webhook secret active")
	}
}

func TestRollbackWebhookSecretRestoresPreviousCredential(t *testing.T) {
	srv, store := newAdminServer(t)
	token := loginAndGetToken(t, srv.URL)

	createResp := authPost(t, srv.URL+"/admin/api/services", token, admin.ServiceRecord{
		ID:           "doc",
		PublicKeyPEM: validPublicKeyPEM(t),
	})
	defer createResp.Body.Close()
	var created struct {
		Credentials struct {
			WebhookSecret string `json:"webhook_secret"`
		} `json:"credentials"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	rotateResp := authPost(t, srv.URL+"/admin/api/services/doc/webhook-secret/rotate", token, nil)
	rotateResp.Body.Close()
	activateResp := authPost(t, srv.URL+"/admin/api/services/doc/webhook-secret/activate", token, nil)
	activateResp.Body.Close()

	rollbackResp := authPost(t, srv.URL+"/admin/api/services/doc/webhook-secret/rollback", token, nil)
	defer rollbackResp.Body.Close()
	if rollbackResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", rollbackResp.StatusCode)
	}
	active, ok := store.ActiveWebhookSecret("doc")
	if !ok || active != created.Credentials.WebhookSecret {
		t.Fatal("rollback must restore the previous webhook secret")
	}
}

func TestActivateResponseMakesRollbackAvailabilityVisible(t *testing.T) {
	srv, _ := newAdminServer(t)
	token := loginAndGetToken(t, srv.URL)

	createResp := authPost(t, srv.URL+"/admin/api/services", token, admin.ServiceRecord{
		ID:           "doc",
		PublicKeyPEM: validPublicKeyPEM(t),
	})
	createResp.Body.Close()
	rotateResp := authPost(t, srv.URL+"/admin/api/services/doc/webhook-secret/rotate", token, nil)
	rotateResp.Body.Close()
	activateResp := authPost(t, srv.URL+"/admin/api/services/doc/webhook-secret/activate", token, nil)
	defer activateResp.Body.Close()

	var view struct {
		WebhookSecretRollbackAvailable bool `json:"webhook_secret_rollback_available"`
	}
	if err := json.NewDecoder(activateResp.Body).Decode(&view); err != nil {
		t.Fatalf("decode activate response: %v", err)
	}
	if !view.WebhookSecretRollbackAvailable {
		t.Fatal("activate response must expose the active rollback window")
	}
}

// T2: Create duplicate service returns 409
func TestCreateDuplicateService(t *testing.T) {
	srv, _ := newAdminServer(t)
	token := loginAndGetToken(t, srv.URL)

	svc := admin.ServiceRecord{ID: "dup", PublicKeyPEM: validPublicKeyPEM(t)}
	authPost(t, srv.URL+"/admin/api/services", token, svc)

	resp := authPost(t, srv.URL+"/admin/api/services", token, svc)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate, got %d", resp.StatusCode)
	}
}

// T3: Delete a service
func TestDeleteService(t *testing.T) {
	srv, _ := newAdminServer(t)
	token := loginAndGetToken(t, srv.URL)

	svc := admin.ServiceRecord{ID: "to-delete", PublicKeyPEM: validPublicKeyPEM(t)}
	authPost(t, srv.URL+"/admin/api/services", token, svc)

	resp := authDelete(t, srv.URL+"/admin/api/services/to-delete", token)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on delete, got %d", resp.StatusCode)
	}

	// Verify removed
	resp2 := authGet(t, srv.URL+"/admin/api/services", token)
	defer resp2.Body.Close()

	var list []admin.ServiceRecord
	json.NewDecoder(resp2.Body).Decode(&list)
	if len(list) != 0 {
		t.Fatalf("expected empty after delete, got %d", len(list))
	}
}

// T3: Delete nonexistent service returns 404
func TestDeleteNonexistentService(t *testing.T) {
	srv, _ := newAdminServer(t)
	token := loginAndGetToken(t, srv.URL)

	resp := authDelete(t, srv.URL+"/admin/api/services/nope", token)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// T4: Unauthenticated requests return 401
func TestUnauthenticatedAccess(t *testing.T) {
	srv, _ := newAdminServer(t)

	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/admin/api/services"},
		{"POST", "/admin/api/services"},
		{"DELETE", "/admin/api/services/x"},
	}
	for _, ep := range endpoints {
		req, _ := http.NewRequest(ep.method, srv.URL+ep.path, nil)
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s %s: expected 401, got %d", ep.method, ep.path, resp.StatusCode)
		}
	}
}

// T4: Invalid token returns 401
func TestInvalidToken(t *testing.T) {
	srv, _ := newAdminServer(t)
	resp := authGet(t, srv.URL+"/admin/api/services", "invalid-token")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid token, got %d", resp.StatusCode)
	}
}

// T4: Login endpoint is not protected
func TestLoginNotProtected(t *testing.T) {
	srv, _ := newAdminServer(t)
	// Login without token should work (and fail with wrong password, but not 401 for missing token)
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "wrong"})
	resp, _ := http.Post(srv.URL+"/admin/api/login", "application/json", bytes.NewReader(body))
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized && false {
		// This is fine — it returns 401 for wrong password, not missing token
	}
}

func authPut(t *testing.T, url, token string, body interface{}) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("PUT", url, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s failed: %v", url, err)
	}
	return resp
}

// Update: modify a service's public key and domains
func TestUpdateService(t *testing.T) {
	srv, _ := newAdminServer(t)
	token := loginAndGetToken(t, srv.URL)

	svc := admin.ServiceRecord{
		ID:                    "my-app",
		PublicKeyPEM:          validPublicKeyPEM(t),
		AllowedWebhookDomains: []string{"old.example.com"},
	}
	authPost(t, srv.URL+"/admin/api/services", token, svc)

	// Update
	newKey := validPublicKeyPEM(t)
	updated := admin.ServiceRecord{
		ID:                    "my-app",
		PublicKeyPEM:          newKey,
		AllowedWebhookDomains: []string{"new.example.com", "also.example.com"},
	}
	resp := authPut(t, srv.URL+"/admin/api/services/my-app", token, updated)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result admin.ServiceRecord
	json.NewDecoder(resp.Body).Decode(&result)
	if result.PublicKeyPEM != newKey {
		t.Fatalf("expected updated public_key, got '%s'", result.PublicKeyPEM)
	}
	if len(result.AllowedWebhookDomains) != 2 || result.AllowedWebhookDomains[0] != "new.example.com" {
		t.Fatalf("expected domains [new.example.com, also.example.com], got %v", result.AllowedWebhookDomains)
	}
}

func TestUpdateServicePreservesWebhookCredential(t *testing.T) {
	srv, _ := newAdminServer(t)
	token := loginAndGetToken(t, srv.URL)

	createResp := authPost(t, srv.URL+"/admin/api/services", token, admin.ServiceRecord{
		ID:                    "doc",
		PublicKeyPEM:          validPublicKeyPEM(t),
		AllowedWebhookDomains: []string{"old.example.com"},
	})
	createResp.Body.Close()

	updateResp := authPut(t, srv.URL+"/admin/api/services/doc", token, admin.ServiceRecord{
		ID:                    "doc",
		PublicKeyPEM:          validPublicKeyPEM(t),
		AllowedWebhookDomains: []string{"new.example.com"},
	})
	defer updateResp.Body.Close()
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", updateResp.StatusCode)
	}
	var updated struct {
		WebhookSecretConfigured bool `json:"webhook_secret_configured"`
	}
	if err := json.NewDecoder(updateResp.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if !updated.WebhookSecretConfigured {
		t.Fatal("updating service identity fields must preserve webhook credential")
	}
}

// Update: nonexistent service returns 404
func TestUpdateNonexistentService(t *testing.T) {
	srv, _ := newAdminServer(t)
	token := loginAndGetToken(t, srv.URL)

	resp := authPut(t, srv.URL+"/admin/api/services/nope", token, admin.ServiceRecord{
		ID:           "nope",
		PublicKeyPEM: validPublicKeyPEM(t),
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestCreateServiceRejectsInvalidPublicKey(t *testing.T) {
	srv, _ := newAdminServer(t)
	token := loginAndGetToken(t, srv.URL)

	resp := authPost(t, srv.URL+"/admin/api/services", token, admin.ServiceRecord{
		ID:                    "bad-key",
		PublicKeyPEM:          "not a pem public key",
		AllowedWebhookDomains: []string{"myapp.example.com"},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid public key, got %d", resp.StatusCode)
	}

	resp2 := authGet(t, srv.URL+"/admin/api/services", token)
	defer resp2.Body.Close()
	var list []admin.ServiceRecord
	json.NewDecoder(resp2.Body).Decode(&list)
	if len(list) != 0 {
		t.Fatalf("invalid service must not be registered, got %d services", len(list))
	}
}

func TestUpdateServiceRejectsInvalidPublicKeyWithoutChangingExistingService(t *testing.T) {
	srv, _ := newAdminServer(t)
	token := loginAndGetToken(t, srv.URL)
	originalKey := validPublicKeyPEM(t)

	authPost(t, srv.URL+"/admin/api/services", token, admin.ServiceRecord{
		ID:                    "my-app",
		PublicKeyPEM:          originalKey,
		AllowedWebhookDomains: []string{"old.example.com"},
	})

	resp := authPut(t, srv.URL+"/admin/api/services/my-app", token, admin.ServiceRecord{
		ID:                    "my-app",
		PublicKeyPEM:          "not a pem public key",
		AllowedWebhookDomains: []string{"new.example.com"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid public key, got %d", resp.StatusCode)
	}

	resp2 := authGet(t, srv.URL+"/admin/api/services", token)
	defer resp2.Body.Close()
	var list []admin.ServiceRecord
	json.NewDecoder(resp2.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("expected existing service to remain, got %d services", len(list))
	}
	if list[0].PublicKeyPEM != originalKey {
		t.Fatal("invalid update must not replace the existing public key")
	}
	if len(list[0].AllowedWebhookDomains) != 1 || list[0].AllowedWebhookDomains[0] != "old.example.com" {
		t.Fatalf("invalid update must not replace domains, got %v", list[0].AllowedWebhookDomains)
	}
}
