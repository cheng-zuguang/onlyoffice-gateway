package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zenmind/onlyoffice-gateway/internal/admin"
	"github.com/zenmind/onlyoffice-gateway/internal/audit"
	"github.com/zenmind/onlyoffice-gateway/internal/storage"
)

func TestAdministratorListsTemporaryAttachments(t *testing.T) {
	attachments, err := storage.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("create attachment store: %v", err)
	}
	if err := attachments.Put(context.Background(), "doc-admin", strings.NewReader("document"), storage.Meta{DocumentID: "doc-admin", ServiceID: "billing", FileName: "invoice.docx", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("put attachment: %v", err)
	}
	auditLog, err := audit.New(t.TempDir(), 14, "test")
	if err != nil {
		t.Fatalf("create audit log: %v", err)
	}
	mux := admin.NewMux(admin.Opts{AdminUsername: "admin", AdminPassword: "secure-password", AdminSessionSecret: "test-admin-secret", Store: admin.NewInMemoryServiceStore(), AttachmentStore: attachments, AuditLog: auditLog})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	token := loginAndGetToken(t, srv.URL)

	resp := authGet(t, srv.URL+"/admin/api/attachments?service_id=billing", token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result struct {
		Items []storage.Meta `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].DocumentID != "doc-admin" {
		t.Fatalf("unexpected attachment list: %#v", result.Items)
	}

	unauth, err := http.Get(srv.URL + "/admin/api/attachments")
	if err != nil {
		t.Fatalf("anonymous request: %v", err)
	}
	defer unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected anonymous request to be rejected, got %d", unauth.StatusCode)
	}
}
