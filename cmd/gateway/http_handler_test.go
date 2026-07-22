package main

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zenmind/onlyoffice-gateway/internal/admin"
	"github.com/zenmind/onlyoffice-gateway/internal/config"
)

func TestRootHandlerLogsAdminAPIRequests(t *testing.T) {
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })

	cfg := &config.Config{
		DocumentServerURL:        "https://doc.example.com",
		DocumentServerJWTSecret:  "test-document-server-secret-0002",
		CallbackCapabilitySecret: "test-callback-capability-secret-02",
		StorageDir:               t.TempDir(),
		WebhookMaxRetries:        3,
	}
	store := admin.NewInMemoryServiceStore()
	handler := newRootHandler(cfg, store, "admin", "admin123")

	req := httptest.NewRequest(http.MethodPost, "/admin/api/login", strings.NewReader(`{"username":"admin","password":"admin123"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected login status 200, got %d", rec.Code)
	}

	output := buf.String()
	if !strings.Contains(output, "[http] POST /admin/api/login 200") {
		t.Fatalf("expected admin login request in access log, got: %s", output)
	}
}

func TestRootHandlerLogsGatewayAPIRequestsOnce(t *testing.T) {
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })

	cfg := &config.Config{
		DocumentServerURL:        "https://doc.example.com",
		DocumentServerJWTSecret:  "test-document-server-secret-0002",
		CallbackCapabilitySecret: "test-callback-capability-secret-02",
		StorageDir:               t.TempDir(),
		WebhookMaxRetries:        3,
	}
	store := admin.NewInMemoryServiceStore()
	handler := newRootHandler(cfg, store, "admin", "admin123")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected health status 200, got %d", rec.Code)
	}

	output := buf.String()
	entry := "[http] GET /api/v1/health 200"
	if count := strings.Count(output, entry); count != 1 {
		t.Fatalf("expected one gateway health access log, got %d in: %s", count, output)
	}
}
