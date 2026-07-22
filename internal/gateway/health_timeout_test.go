package gateway

import (
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/zenmind/onlyoffice-gateway/internal/config"
)

type timeoutTestResolver struct{}

func (timeoutTestResolver) Resolve(string) (*rsa.PublicKey, []string, bool) {
	return nil, nil, false
}

func TestDocumentServerHealthCheckTimesOut(t *testing.T) {
	blockingDS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer blockingDS.Close()

	previousClient := documentServerHTTPClient
	documentServerHTTPClient = &http.Client{Timeout: 25 * time.Millisecond}
	t.Cleanup(func() { documentServerHTTPClient = previousClient })

	cfg := &config.Config{
		DocumentServerURL:        blockingDS.URL,
		DocumentServerJWTSecret:  "test-document-server-secret-0002",
		CallbackCapabilitySecret: "test-callback-capability-secret-02",
		StorageDir:               filepath.Join(t.TempDir(), "storage"),
		TTLHours:                 8,
		WebhookMaxRetries:        3,
	}
	loaded, err := config.FromLiteral(cfg)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	server := httptest.NewServer(NewHandler(loaded, timeoutTestResolver{}))
	t.Cleanup(server.Close)

	start := time.Now()
	resp, err := http.Get(server.URL + "/api/v1/health/ds")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("health check took too long: %s", elapsed)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if ok, _ := result["document_server_ok"].(bool); ok {
		t.Fatalf("expected document_server_ok=false, got: %v", result)
	}
}
