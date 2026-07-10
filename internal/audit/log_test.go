package audit

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestLogWritesRedactedEventThatCanBeQueried(t *testing.T) {
	log, err := New(t.TempDir(), 14, "gateway-test")
	if err != nil {
		t.Fatalf("create audit log: %v", err)
	}
	event := Event{
		Time:       time.Now().UTC(),
		Level:      "info",
		Type:       "admin.attachment_deleted",
		DocumentID: "doc-1",
		URL:        "https://business.example.com/callback?signature=secret",
	}
	if err := log.Write(context.Background(), event); err != nil {
		t.Fatalf("write audit event: %v", err)
	}
	items, _, err := log.List(context.Background(), Query{DocumentID: "doc-1"})
	if err != nil {
		t.Fatalf("query audit event: %v", err)
	}
	if len(items) != 1 || items[0].Type != event.Type {
		t.Fatalf("unexpected audit events: %#v", items)
	}
	if strings.Contains(items[0].URL, "signature") || items[0].URL != "https://business.example.com/callback" {
		t.Fatalf("expected redacted URL, got %q", items[0].URL)
	}
}

func TestLogCursorReturnsRemainingEvents(t *testing.T) {
	log, err := New(t.TempDir(), 14, "gateway-test")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := log.Write(context.Background(), Event{Time: time.Now().Add(time.Duration(i) * time.Second), Level: "info", Type: "callback.saved"}); err != nil {
			t.Fatal(err)
		}
	}
	first, cursor, err := log.List(context.Background(), Query{Limit: 1})
	if err != nil || len(first) != 1 || cursor == "" {
		t.Fatalf("expected first page and cursor, items=%d cursor=%q err=%v", len(first), cursor, err)
	}
	second, final, err := log.List(context.Background(), Query{Limit: 1, Cursor: cursor})
	if err != nil || len(second) != 1 || second[0].Time.Equal(first[0].Time) || final != "" {
		t.Fatalf("expected a distinct final page, items=%#v cursor=%q err=%v", second, final, err)
	}
}
