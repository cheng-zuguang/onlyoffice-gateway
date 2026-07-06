package admin_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zenmind/onlyoffice-gateway/internal/admin"
)

func TestServiceStorePersistsAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "services.json")

	// First "run" — add services
	store1, err := admin.NewPersistentServiceStore(path)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	store1.Add(admin.ServiceRecord{
		ID:                    "svc-a",
		PublicKeyPEM:          "-----BEGIN PUBLIC KEY-----\nkey-a\n-----END PUBLIC KEY-----",
		AllowedWebhookDomains: []string{"a.example.com"},
	})
	store1.Add(admin.ServiceRecord{
		ID:                    "svc-b",
		PublicKeyPEM:          "-----BEGIN PUBLIC KEY-----\nkey-b\n-----END PUBLIC KEY-----",
		AllowedWebhookDomains: []string{"b.example.com"},
	})

	// "Restart" — reload from same file
	store2, err := admin.NewPersistentServiceStore(path)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}

	list := store2.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 services after reload, got %d", len(list))
	}

	svc, ok := store2.Get("svc-a")
	if !ok {
		t.Fatal("expected svc-a to exist after reload")
	}
	if len(svc.AllowedWebhookDomains) != 1 || svc.AllowedWebhookDomains[0] != "a.example.com" {
		t.Fatalf("unexpected svc-a data: %+v", svc)
	}
}

func TestServiceStoreRemovePersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "services.json")

	store1, _ := admin.NewPersistentServiceStore(path)
	store1.Add(admin.ServiceRecord{ID: "svc-a", PublicKeyPEM: "pub-a"})
	store1.Add(admin.ServiceRecord{ID: "svc-b", PublicKeyPEM: "pub-b"})
	store1.Remove("svc-a")

	// Reload
	store2, _ := admin.NewPersistentServiceStore(path)
	list := store2.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 service after remove+reload, got %d", len(list))
	}
	if list[0].ID != "svc-b" {
		t.Fatalf("expected svc-b, got %s", list[0].ID)
	}
}

func TestNewPersistentStoreCreatesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	store, err := admin.NewPersistentServiceStore(path)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if len(store.List()) != 0 {
		t.Fatal("expected empty store")
	}

	// File should exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("expected services.json to be created")
	}
}
