package admin_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zenmind/onlyoffice-gateway/internal/admin"
)

func TestLoginWithValidCredentials(t *testing.T) {
	store := admin.NewInMemoryServiceStore()
	mux := admin.NewMux(admin.Opts{
		AdminUsername: "admin",
		AdminPassword: "secure-password",
		JWTSecret:     "test-admin-secret",
		Store:         store,
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{
		"username": "admin",
		"password": "secure-password",
	})
	resp, err := http.Post(srv.URL+"/admin/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["token"] == "" {
		t.Fatal("expected token in response")
	}
	if result["token_type"] != "Bearer" {
		t.Fatalf("expected Bearer token_type, got %s", result["token_type"])
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	store := admin.NewInMemoryServiceStore()
	mux := admin.NewMux(admin.Opts{
		AdminUsername: "admin",
		AdminPassword: "secure-password",
		JWTSecret:     "test-admin-secret",
		Store:         store,
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{
		"username": "admin",
		"password": "wrong-password",
	})
	resp, _ := http.Post(srv.URL+"/admin/api/login", "application/json", bytes.NewReader(body))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}
