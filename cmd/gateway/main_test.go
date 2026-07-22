package main_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMainStartsAndRespondsToHealth(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "gateway.yaml")
	storageDir := filepath.Join(tmpDir, "storage")
	servicesPath := filepath.Join(tmpDir, "services.json")
	binPath := filepath.Join(tmpDir, "gateway")

	// Write config without services
	cfg := fmt.Sprintf(`listen_addr: "127.0.0.1:18999"
document_server_url: "https://doc.example.com"
document_server_jwt_secret: "test-document-server-secret-0001"
gateway_admin_session_secret: "test-admin-session-secret-00000001"
gateway_callback_capability_secret: "test-callback-capability-secret-01"
webhook_secret_encryption_key: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
storage_dir: "%s"
ttl_hours: 8
webhook_max_retries: 3
`, storageDir)
	os.WriteFile(configPath, []byte(cfg), 0644)

	// Health check does not require registered services; keep the registry empty
	// so the startup test only verifies process boot and routing.
	services := []map[string]interface{}{}
	servicesData, _ := json.MarshalIndent(services, "", "  ")
	os.WriteFile(servicesPath, servicesData, 0644)

	// Build binary
	modRoot := findModuleRoot(t)
	buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/gateway/")
	buildCmd.Dir = modRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	// Start the gateway. Explicitly set all env vars so the test is immune
	// to whatever .env file may exist in the project root.
	cmd := exec.Command(binPath, "-config", configPath)
	cmd.Dir = tmpDir // run from temp dir so it doesn't find project .env
	cmd.Env = append(os.Environ(),
		"LISTEN_ADDR=127.0.0.1:18999",
		"DOCUMENT_SERVER_URL=https://doc.example.com",
		"DOCUMENT_SERVER_JWT_SECRET=test-document-server-secret-0001",
		"GATEWAY_ADMIN_SESSION_SECRET=test-admin-session-secret-00000001",
		"GATEWAY_CALLBACK_CAPABILITY_SECRET=test-callback-capability-secret-01",
		"WEBHOOK_SECRET_ENCRYPTION_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		"SERVICE_STORE_PATH="+servicesPath,
		"ADMIN_USERNAME=admin",
		"ADMIN_PASSWORD=admin123",
		"STORAGE_DIR="+storageDir,
		"TTL_HOURS=8",
		"CLEANUP_INTERVAL=1h",
		"WEBHOOK_MAX_RETRIES=3",
		"HOME="+os.Getenv("HOME"),
		"PATH="+os.Getenv("PATH"),
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

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
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("gateway did not exit gracefully: %v", err)
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
