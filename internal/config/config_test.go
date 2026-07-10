package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/zenmind/onlyoffice-gateway/internal/config"
)

func TestLoadParsesCleanupIntervalFromYAML(t *testing.T) {
	path := t.TempDir() + "/gateway.yaml"
	if err := os.WriteFile(path, []byte("cleanup_interval: 15m\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.CleanupInterval != 15*time.Minute {
		t.Fatalf("expected cleanup interval 15m, got %s", cfg.CleanupInterval)
	}
}

func TestFromLiteralLeavesDocumentServerPublicURLEmptyByDefault(t *testing.T) {
	cfg, err := config.FromLiteral(&config.Config{
		DocumentServerURL: "http://document-server",
		StorageDir:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.DocumentServerPublicURL != "" {
		t.Fatalf("expected empty public URL by default, got %q", cfg.DocumentServerPublicURL)
	}
}

func TestFromLiteralReadsDocumentServerPublicURLFromEnvironment(t *testing.T) {
	t.Setenv("DOCUMENT_SERVER_PUBLIC_URL", "https://office.example.com")

	cfg, err := config.FromLiteral(&config.Config{
		DocumentServerURL: "http://document-server",
		StorageDir:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.DocumentServerPublicURL != "https://office.example.com" {
		t.Fatalf("expected env public URL, got %q", cfg.DocumentServerPublicURL)
	}
}

func TestFromLiteralDefaultsStorageBackendToLocal(t *testing.T) {
	cfg, err := config.FromLiteral(&config.Config{
		DocumentServerURL: "http://document-server",
		StorageDir:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.StorageBackend != "local" {
		t.Fatalf("expected local storage backend by default, got %q", cfg.StorageBackend)
	}
}

func TestFromLiteralReadsS3StorageEnvironment(t *testing.T) {
	t.Setenv("STORAGE_BACKEND", "s3")
	t.Setenv("S3_ENDPOINT", "http://minio:9000/")
	t.Setenv("S3_REGION", "us-west-2")
	t.Setenv("S3_BUCKET", "onlyoffice")
	t.Setenv("S3_ACCESS_KEY", "access")
	t.Setenv("S3_SECRET_KEY", "secret")
	t.Setenv("S3_USE_PATH_STYLE", "true")
	t.Setenv("S3_USE_SSL", "false")
	t.Setenv("S3_PREFIX", "/documents/")

	cfg, err := config.FromLiteral(&config.Config{
		DocumentServerURL: "http://document-server",
		StorageDir:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.StorageBackend != "s3" || cfg.S3Endpoint != "http://minio:9000" || cfg.S3Region != "us-west-2" || cfg.S3Bucket != "onlyoffice" {
		t.Fatalf("unexpected s3 config: %+v", cfg)
	}
	if !cfg.S3UsePathStyle || cfg.S3UseSSL {
		t.Fatalf("unexpected s3 bool config: path_style=%v ssl=%v", cfg.S3UsePathStyle, cfg.S3UseSSL)
	}
	if cfg.S3Prefix != "documents" {
		t.Fatalf("expected normalized prefix documents, got %q", cfg.S3Prefix)
	}
}

func TestFromLiteralReadsCallbackQueueEnvironment(t *testing.T) {
	t.Setenv("CALLBACK_QUEUE_SIZE", "128")
	t.Setenv("CALLBACK_WORKERS", "8")

	cfg, err := config.FromLiteral(&config.Config{
		DocumentServerURL: "http://document-server",
		StorageDir:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.CallbackQueueSize != 128 {
		t.Fatalf("expected callback queue size 128, got %d", cfg.CallbackQueueSize)
	}
	if cfg.CallbackWorkers != 8 {
		t.Fatalf("expected callback workers 8, got %d", cfg.CallbackWorkers)
	}
}

func TestValidateRejectsMissingJWTSecret(t *testing.T) {
	err := (&config.Config{
		DocumentServerURL: "https://docs.example.com",
		StorageBackend:    "local",
		StorageDir:        t.TempDir(),
		TTLHours:          8,
	}).Validate()
	if err == nil {
		t.Fatal("expected configuration without JWT_SECRET to be rejected")
	}
}
