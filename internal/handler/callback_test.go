package handler

import (
	"net"
	"net/http"
	"testing"
	"time"
)

func TestCallbackHTTPClientUsesTunedTransport(t *testing.T) {
	transport, ok := callbackHTTPClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected callback HTTP client to use *http.Transport, got %T", callbackHTTPClient.Transport)
	}
	if transport.MaxIdleConns < 100 {
		t.Fatalf("expected MaxIdleConns >= 100, got %d", transport.MaxIdleConns)
	}
	if transport.MaxIdleConnsPerHost < 20 {
		t.Fatalf("expected MaxIdleConnsPerHost >= 20, got %d", transport.MaxIdleConnsPerHost)
	}
	if transport.IdleConnTimeout < 30*time.Second {
		t.Fatalf("expected IdleConnTimeout >= 30s, got %s", transport.IdleConnTimeout)
	}
	if transport.ResponseHeaderTimeout < 5*time.Second {
		t.Fatalf("expected ResponseHeaderTimeout >= 5s, got %s", transport.ResponseHeaderTimeout)
	}
	if transport.DialContext == nil {
		t.Fatal("expected DialContext to be configured")
	}
	_ = (&net.Dialer{}).Timeout
}
