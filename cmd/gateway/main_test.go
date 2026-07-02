package main_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestMainStartsAndRespondsToHealth(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "gateway.yaml")
	storageDir := filepath.Join(tmpDir, "storage")
	binPath := filepath.Join(tmpDir, "gateway")

	// Generate valid RSA key
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pubBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal pub: %v", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes})

	// Write config
	cfg := fmt.Sprintf(`listen_addr: "127.0.0.1:18999"
document_server_url: "https://doc.example.com"
jwt_secret: "test-secret"
storage_dir: "%s"
ttl_hours: 8
webhook_max_retries: 3
services:
  - id: "test-service"
    public_key: |
%s
    allowed_webhook_domains:
      - "test.example.com"
`, storageDir, indentLines(string(pubPEM), 6))
	os.WriteFile(configPath, []byte(cfg), 0644)

	// Build binary from module root
	modRoot := findModuleRoot(t)
	buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/gateway/")
	buildCmd.Dir = modRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	// Start the gateway
	cmd := exec.Command(binPath, "-config", configPath)
	cmd.Dir = modRoot
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })

	// Wait for server
	var resp *http.Response
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		r, err := http.Get("http://127.0.0.1:18999/api/v1/health")
		if err == nil {
			resp = r
			break
		}
	}

	if resp == nil {
		t.Fatal("server did not start within 3 seconds")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("cannot find go.mod")
		}
		dir = parent
	}
}

func indentLines(s string, spaces int) string {
	prefix := ""
	for range spaces {
		prefix += " "
	}
	result := ""
	lines := splitLines(s)
	for i, line := range lines {
		if i > 0 {
			result += "\n"
		}
		if line != "" {
			result += prefix + line
		}
	}
	return result
}

func splitLines(s string) []string {
	var lines []string
	current := ""
	for _, c := range s {
		if c == '\n' {
			lines = append(lines, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	lines = append(lines, current)
	return lines
}
