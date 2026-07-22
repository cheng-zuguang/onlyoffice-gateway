package admin_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/zenmind/onlyoffice-gateway/internal/admin"
)

func TestWebhookSecretPersistsEncryptedAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "services.json")
	key := bytes.Repeat([]byte{0x42}, 32)

	store1, err := admin.NewPersistentServiceStoreWithEncryptionKey(path, key)
	if err != nil {
		t.Fatalf("create encrypted store: %v", err)
	}
	secret, err := store1.CreateWithWebhookCredential(admin.ServiceRecord{
		ID:                    "doc",
		PublicKeyPEM:          validPublicKeyPEM(t),
		AllowedWebhookDomains: []string{"doc.example.com"},
	})
	if err != nil {
		t.Fatalf("create service: %v", err)
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read service store: %v", err)
	}
	if bytes.Contains(onDisk, []byte(secret)) {
		t.Fatal("services.json must not contain the plaintext webhook secret")
	}

	store2, err := admin.NewPersistentServiceStoreWithEncryptionKey(path, key)
	if err != nil {
		t.Fatalf("reload encrypted store: %v", err)
	}
	reloaded, ok := store2.ActiveWebhookSecret("doc")
	if !ok {
		t.Fatal("expected active webhook secret after restart")
	}
	if reloaded != secret {
		t.Fatal("reloaded webhook secret does not match the generated credential")
	}
}

func TestEncryptedWebhookSecretRejectsWrongMasterKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.json")
	store, err := admin.NewPersistentServiceStoreWithEncryptionKey(path, bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("create encrypted store: %v", err)
	}
	if _, err := store.CreateWithWebhookCredential(admin.ServiceRecord{
		ID:           "doc",
		PublicKeyPEM: validPublicKeyPEM(t),
	}); err != nil {
		t.Fatalf("create service: %v", err)
	}

	if _, err := admin.NewPersistentServiceStoreWithEncryptionKey(path, bytes.Repeat([]byte{0x43}, 32)); err == nil {
		t.Fatal("loading encrypted credentials with a different master key must fail")
	}
}

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
		PublicKeyPEM:          validPublicKeyPEM(t),
		AllowedWebhookDomains: []string{"a.example.com"},
	})
	store1.Add(admin.ServiceRecord{
		ID:                    "svc-b",
		PublicKeyPEM:          validPublicKeyPEM(t),
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
	store1.Add(admin.ServiceRecord{ID: "svc-a", PublicKeyPEM: validPublicKeyPEM(t)})
	store1.Add(admin.ServiceRecord{ID: "svc-b", PublicKeyPEM: validPublicKeyPEM(t)})
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
