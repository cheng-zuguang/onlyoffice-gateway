package storage_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zenmind/onlyoffice-gateway/internal/config"
	"github.com/zenmind/onlyoffice-gateway/internal/storage"
)

func TestNewStoreDefaultsToLocal(t *testing.T) {
	cfg, err := config.FromLiteral(&config.Config{
		DocumentServerURL: "http://document-server",
		StorageDir:        filepath.Join(t.TempDir(), "storage"),
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	store, err := storage.NewStore(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, ok := store.(*storage.LocalStore); !ok {
		t.Fatalf("expected local store, got %T", store)
	}
}

func TestNewStoreRejectsS3WithoutBucket(t *testing.T) {
	cfg, err := config.FromLiteral(&config.Config{
		DocumentServerURL: "http://document-server",
		StorageBackend:    "s3",
		StorageDir:        filepath.Join(t.TempDir(), "storage"),
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	_, err = storage.NewStore(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected s3 store creation to fail without bucket")
	}
}
