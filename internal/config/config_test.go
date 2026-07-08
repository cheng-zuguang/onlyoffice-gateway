package config_test

import (
	"testing"

	"github.com/zenmind/onlyoffice-gateway/internal/config"
)

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
