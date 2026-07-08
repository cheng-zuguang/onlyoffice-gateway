package gateway_test

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zenmind/onlyoffice-gateway/internal/gateway"
)

// Tracer bullet: wrapping a handler with LoggingMiddleware logs
// the HTTP method, request path, response status code, and duration.
func TestLoggingMiddlewareLogsRequestInfo(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(log.Writer()) })

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := gateway.LoggingMiddleware(inner)
	server := httptest.NewServer(wrapped)
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	output := buf.String()

	if !strings.Contains(output, "GET") {
		t.Fatalf("expected log to contain HTTP method GET, got: %s", output)
	}
	if !strings.Contains(output, "/api/v1/health") {
		t.Fatalf("expected log to contain path /api/v1/health, got: %s", output)
	}
	if !strings.Contains(output, "200") {
		t.Fatalf("expected log to contain status 200, got: %s", output)
	}
}

func TestLoggingMiddlewareLogsPOSTAnd4xx(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(log.Writer()) })

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	wrapped := gateway.LoggingMiddleware(inner)
	server := httptest.NewServer(wrapped)
	t.Cleanup(server.Close)

	body := bytes.NewReader([]byte(`{"test":true}`))
	resp, err := http.Post(server.URL+"/api/v1/documents", "application/json", body)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	output := buf.String()

	if !strings.Contains(output, "POST") {
		t.Fatalf("expected log to contain POST, got: %s", output)
	}
	if !strings.Contains(output, "/api/v1/documents") {
		t.Fatalf("expected log to contain /api/v1/documents, got: %s", output)
	}
	if !strings.Contains(output, "401") {
		t.Fatalf("expected log to contain status 401, got: %s", output)
	}
}

func TestLoggingMiddlewareIncludesDuration(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(log.Writer()) })

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := gateway.LoggingMiddleware(inner)
	server := httptest.NewServer(wrapped)
	t.Cleanup(server.Close)

	http.Get(server.URL + "/api/v1/health")

	output := buf.String()

	// Duration should appear near the end, e.g. "12ms" or "123µs"
	if !strings.Contains(output, "µs") && !strings.Contains(output, "ms") && !strings.Contains(output, "s") {
		t.Fatalf("expected log to contain duration unit (µs/ms/s), got: %s", output)
	}
}

func TestLoggingMiddlewareLogsFirstStatusCode(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(log.Writer()) })

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.WriteHeader(http.StatusInternalServerError)
	})

	wrapped := gateway.LoggingMiddleware(inner)
	server := httptest.NewServer(wrapped)
	t.Cleanup(server.Close)

	resp, err := http.Post(server.URL+"/api/v1/documents", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	output := buf.String()
	if !strings.Contains(output, "201") {
		t.Fatalf("expected log to contain first status 201, got: %s", output)
	}
	if strings.Contains(output, "500") {
		t.Fatalf("expected log not to be overwritten by second status, got: %s", output)
	}
}

func TestLoggingMiddlewareIncludesRequestContext(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(log.Writer()) })

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := gateway.LoggingMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.RemoteAddr = "203.0.113.10:54321"
	req.Header.Set("User-Agent", "gateway-test/1.0")
	req.Header.Set("X-Request-Id", "req-123")
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	output := buf.String()
	for _, want := range []string{
		"remote_addr=203.0.113.10:54321",
		`user_agent="gateway-test/1.0"`,
		"request_id=req-123",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected log to contain %q, got: %s", want, output)
		}
	}
}
