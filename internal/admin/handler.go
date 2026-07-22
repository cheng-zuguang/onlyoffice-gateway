package admin

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/zenmind/onlyoffice-gateway/internal/audit"
	"github.com/zenmind/onlyoffice-gateway/internal/storage"
)

// Opts holds dependencies for the admin mux.
type Opts struct {
	AdminUsername      string
	AdminPassword      string
	AdminSessionSecret string
	Store              *InMemoryServiceStore
	AttachmentStore    storage.Store
	AuditLog           *audit.Log
}

// NewMux returns an http.Handler mounting all admin API routes.
func NewMux(opts Opts) http.Handler {
	mux := http.NewServeMux()
	auth := &authHandler{
		username: opts.AdminUsername,
		password: opts.AdminPassword,
		jwtKey:   []byte(opts.AdminSessionSecret),
	}
	mux.HandleFunc("POST /admin/api/login", auth.handleLogin)

	svc := &serviceHandler{store: opts.Store, audit: opts.AuditLog}
	protect := func(h http.HandlerFunc) http.HandlerFunc {
		return auth.middleware(h)
	}
	mux.HandleFunc("GET /admin/api/services", protect(svc.handleListServices))
	mux.HandleFunc("POST /admin/api/services", protect(svc.handleCreateService))
	mux.HandleFunc("PUT /admin/api/services/{id}", protect(svc.handleUpdateService))
	mux.HandleFunc("DELETE /admin/api/services/{id}", protect(svc.handleDeleteService))
	mux.HandleFunc("POST /admin/api/services/{id}/webhook-secret/rotate", protect(svc.handleRotateWebhookSecret))
	mux.HandleFunc("POST /admin/api/services/{id}/webhook-secret/activate", protect(svc.handleActivateWebhookSecret))
	mux.HandleFunc("POST /admin/api/services/{id}/webhook-secret/rollback", protect(svc.handleRollbackWebhookSecret))
	attachments := &attachmentHandler{store: opts.AttachmentStore, audit: opts.AuditLog}
	mux.HandleFunc("GET /admin/api/attachments", protect(attachments.handleList))
	mux.HandleFunc("GET /admin/api/attachments/{id}", protect(attachments.handleGet))
	mux.HandleFunc("GET /admin/api/attachments/{id}/download", protect(attachments.handleDownload))
	mux.HandleFunc("POST /admin/api/attachments/{id}/extend-ttl", protect(attachments.handleExtendTTL))
	mux.HandleFunc("DELETE /admin/api/attachments/{id}", protect(attachments.handleDelete))
	mux.HandleFunc("POST /admin/api/attachments/cleanup", protect(attachments.handleCleanup))
	mux.HandleFunc("GET /admin/api/logs", protect(attachments.handleLogs))

	return mux
}

// AttachmentRecord is the safe administrator representation of a temporary
// attachment. It deliberately excludes callback and direct-source URLs.
type AttachmentRecord struct {
	DocumentID   string    `json:"document_id"`
	ServiceID    string    `json:"service_id"`
	ExternalID   string    `json:"external_id,omitempty"`
	FileName     string    `json:"file_name"`
	DocumentType string    `json:"document_type"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	IsEdited     bool      `json:"is_edited"`
	EditedAt     time.Time `json:"edited_at,omitempty"`
	DirectSource bool      `json:"direct_source"`
	SourceHost   string    `json:"source_host,omitempty"`
	WebhookHost  string    `json:"webhook_host,omitempty"`
}

type attachmentHandler struct {
	store storage.Store
	audit *audit.Log
}

func (h *attachmentHandler) available(w http.ResponseWriter) bool {
	if h.store == nil || h.audit == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "attachment administration unavailable"})
		return false
	}
	return true
}

func (h *attachmentHandler) handleList(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, next, err := h.store.List(r.Context(), storage.AttachmentQuery{ServiceID: r.URL.Query().Get("service_id"), Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid attachment query"})
		return
	}
	records := make([]AttachmentRecord, 0, len(items))
	for _, item := range items {
		records = append(records, attachmentRecord(item))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": records, "next_cursor": next})
}

func (h *attachmentHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	meta, err := h.store.GetMeta(r.Context(), r.PathValue("id"))
	if err != nil {
		attachmentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, attachmentRecord(*meta))
}

func (h *attachmentHandler) handleDownload(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	id := r.PathValue("id")
	meta, err := h.store.GetMeta(r.Context(), id)
	if err != nil {
		attachmentError(w, err)
		return
	}
	if meta.SourceURL != "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "direct-source attachment is not hosted by gateway"})
		return
	}
	variant := storage.Variant(r.URL.Query().Get("variant"))
	if variant == "" {
		variant = storage.VariantLatest
	}
	if variant != storage.VariantOriginal && variant != storage.VariantLatest {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid variant"})
		return
	}
	reader, _, info, err := h.store.Open(r.Context(), id, variant, nil)
	if err != nil {
		attachmentError(w, err)
		return
	}
	defer reader.Close()
	if err := h.audit.Write(r.Context(), audit.Event{Level: "info", Type: "admin.attachment_downloaded", DocumentID: id}); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "audit log unavailable"})
		return
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": meta.FileName}))
	w.Header().Set("Content-Type", info.ContentType)
	io.Copy(w, reader)
}

func (h *attachmentHandler) handleExtendTTL(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	var body struct {
		Hours int `json:"hours"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Hours < 1 || body.Hours > 168 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hours must be between 1 and 168"})
		return
	}
	id := r.PathValue("id")
	if err := h.audit.Write(r.Context(), audit.Event{Level: "info", Type: "admin.attachment_ttl_extended", DocumentID: id}); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "audit log unavailable"})
		return
	}
	if err := h.store.ExtendTTL(r.Context(), id, body.Hours); err != nil {
		attachmentError(w, err)
		return
	}
	meta, _ := h.store.GetMeta(r.Context(), id)
	writeJSON(w, http.StatusOK, attachmentRecord(*meta))
}

func (h *attachmentHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || !body.Confirm {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "confirm must be true"})
		return
	}
	id := r.PathValue("id")
	if err := h.audit.Write(r.Context(), audit.Event{Level: "info", Type: "admin.attachment_deleted", DocumentID: id}); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "audit log unavailable"})
		return
	}
	if err := h.store.Delete(r.Context(), id); err != nil {
		attachmentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (h *attachmentHandler) handleCleanup(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	if err := h.audit.Write(r.Context(), audit.Event{Level: "info", Type: "admin.attachments_cleanup"}); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "audit log unavailable"})
		return
	}
	count, err := h.store.Expire(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cleanup failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"cleaned": count})
}

func (h *attachmentHandler) handleLogs(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, next, err := h.audit.List(r.Context(), audit.Query{Level: r.URL.Query().Get("level"), Type: r.URL.Query().Get("type"), RequestID: r.URL.Query().Get("request_id"), DocumentID: r.URL.Query().Get("document_id"), Limit: limit, Cursor: r.URL.Query().Get("cursor")})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read audit log"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": items, "next_cursor": next})
}

func attachmentRecord(meta storage.Meta) AttachmentRecord {
	return AttachmentRecord{DocumentID: meta.DocumentID, ServiceID: meta.ServiceID, ExternalID: meta.ExternalID, FileName: meta.FileName, DocumentType: meta.DocumentType, CreatedAt: meta.CreatedAt, ExpiresAt: meta.ExpiresAt, IsEdited: meta.IsEdited, EditedAt: meta.EditedAt, DirectSource: meta.SourceURL != "", SourceHost: hostOf(meta.SourceURL), WebhookHost: hostOf(meta.WebhookURL)}
}
func hostOf(raw string) string {
	if raw == "" {
		return ""
	}
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	return strings.Split(strings.Split(strings.Split(raw, "/")[0], "?")[0], ":")[0]
}
func attachmentError(w http.ResponseWriter, err error) {
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "attachment not found"})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "attachment storage error"})
}

// ── Auth ──────────────────────────────────────────────────────────────────────

type authHandler struct {
	username string
	password string
	jwtKey   []byte
}

func (h *authHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Username), []byte(h.username)) != 1 ||
		subtle.ConstantTimeCompare([]byte(req.Password), []byte(h.password)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "admin",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(24 * time.Hour).Unix(),
	})
	signed, err := token.SignedString(h.jwtKey)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to sign token"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"token":      signed,
		"token_type": "Bearer",
	})
}

func (h *authHandler) middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing authorization header"})
			return
		}
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenStr == authHeader {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid authorization format"})
			return
		}
		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method")
			}
			return h.jwtKey, nil
		})
		if err != nil || !token.Valid {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
			return
		}
		next(w, r)
	}
}

// ── Services CRUD ─────────────────────────────────────────────────────────────

type serviceHandler struct {
	store *InMemoryServiceStore
	audit *audit.Log
}

func (h *serviceHandler) writeCredentialAudit(r *http.Request, serviceID, eventType string) error {
	if h.audit == nil {
		return nil
	}
	return h.audit.Write(r.Context(), audit.Event{
		Level:     "info",
		Type:      eventType,
		ServiceID: serviceID,
	})
}

type serviceView struct {
	ID                             string    `json:"id"`
	PublicKeyPEM                   string    `json:"public_key"`
	AllowedWebhookDomains          []string  `json:"allowed_webhook_domains"`
	WebhookSecretConfigured        bool      `json:"webhook_secret_configured"`
	WebhookSecretLastRotatedAt     time.Time `json:"webhook_secret_last_rotated_at,omitempty"`
	WebhookSecretPending           bool      `json:"webhook_secret_pending"`
	WebhookSecretRollbackAvailable bool      `json:"webhook_secret_rollback_available"`
}

func viewService(svc ServiceRecord) serviceView {
	return serviceView{
		ID:                         svc.ID,
		PublicKeyPEM:               svc.PublicKeyPEM,
		AllowedWebhookDomains:      append([]string(nil), svc.AllowedWebhookDomains...),
		WebhookSecretConfigured:    svc.webhookSecret != "",
		WebhookSecretLastRotatedAt: svc.webhookSecretCreatedAt,
		WebhookSecretPending:       svc.pendingWebhookSecret != "",
		WebhookSecretRollbackAvailable: svc.previousWebhookSecret != "" &&
			time.Now().UTC().Before(svc.previousSecretExpiresAt),
	}
}

func (h *serviceHandler) handleListServices(w http.ResponseWriter, r *http.Request) {
	services := h.store.List()
	views := make([]serviceView, 0, len(services))
	for _, svc := range services {
		views = append(views, viewService(svc))
	}
	writeJSON(w, http.StatusOK, views)
}

func (h *serviceHandler) handleCreateService(w http.ResponseWriter, r *http.Request) {
	var svc ServiceRecord
	if err := json.NewDecoder(r.Body).Decode(&svc); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if svc.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id is required"})
		return
	}
	secret, err := h.store.CreateWithWebhookCredential(svc)
	if err != nil {
		if err == errServiceExists {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "service id already exists"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	created, ok := h.store.Get(svc.ID)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "service was not created"})
		return
	}
	if err := h.writeCredentialAudit(r, svc.ID, "admin.service_webhook_credential_created"); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "audit log unavailable"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"service": viewService(*created),
		"credentials": map[string]string{
			"webhook_secret": secret,
		},
	})
}

func (h *serviceHandler) handleUpdateService(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var svc ServiceRecord
	if err := json.NewDecoder(r.Body).Decode(&svc); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	svc.ID = id
	if err := h.store.Update(id, svc); err != nil {
		if err == errServiceNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	updated, _ := h.store.Get(id)
	writeJSON(w, http.StatusOK, viewService(*updated))
}

func (h *serviceHandler) handleDeleteService(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.Delete(id); err != nil {
		if err == errServiceNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "service not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (h *serviceHandler) handleRotateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	secret, err := h.store.RotateWebhookSecret(r.PathValue("id"), time.Now().UTC())
	if err != nil {
		switch err {
		case errServiceNotFound:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "service not found"})
		case errWebhookSecretPending:
			writeJSON(w, http.StatusConflict, map[string]string{"error": "pending webhook secret already exists"})
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return
	}
	if err := h.writeCredentialAudit(r, r.PathValue("id"), "admin.service_webhook_credential_rotation_pending"); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "audit log unavailable"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"service_id": r.PathValue("id"),
		"credentials": map[string]string{
			"webhook_secret": secret,
		},
	})
}

func (h *serviceHandler) handleActivateWebhookSecret(w http.ResponseWriter, r *http.Request) {
	if err := h.store.ActivateWebhookSecret(r.PathValue("id"), time.Now().UTC()); err != nil {
		switch err {
		case errServiceNotFound:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "service not found"})
		case errNoPendingWebhookSecret:
			writeJSON(w, http.StatusConflict, map[string]string{"error": "pending webhook secret not found"})
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return
	}
	if err := h.writeCredentialAudit(r, r.PathValue("id"), "admin.service_webhook_credential_activated"); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "audit log unavailable"})
		return
	}
	svc, _ := h.store.Get(r.PathValue("id"))
	writeJSON(w, http.StatusOK, viewService(*svc))
}

func (h *serviceHandler) handleRollbackWebhookSecret(w http.ResponseWriter, r *http.Request) {
	if err := h.store.RollbackWebhookSecret(r.PathValue("id"), time.Now().UTC()); err != nil {
		switch err {
		case errServiceNotFound:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "service not found"})
		case errWebhookRollbackUnavailable:
			writeJSON(w, http.StatusConflict, map[string]string{"error": "webhook secret rollback unavailable"})
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return
	}
	if err := h.writeCredentialAudit(r, r.PathValue("id"), "admin.service_webhook_credential_rolled_back"); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "audit log unavailable"})
		return
	}
	svc, _ := h.store.Get(r.PathValue("id"))
	writeJSON(w, http.StatusOK, viewService(*svc))
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// ── Service Store ─────────────────────────────────────────────────────────────

// InMemoryServiceStore holds service records in memory, safe for concurrent use.
// When a path is set, Add and Remove persist to disk.
type InMemoryServiceStore struct {
	mu            sync.RWMutex
	services      []ServiceRecord
	publicKeys    map[string]*rsa.PublicKey
	path          string
	encryptionKey []byte
}

// ServiceRecord represents one external service authorized to use the gateway.
type ServiceRecord struct {
	ID                      string                  `json:"id"`
	PublicKeyPEM            string                  `json:"public_key"`
	AllowedWebhookDomains   []string                `json:"allowed_webhook_domains"`
	WebhookCredentials      *WebhookCredentialState `json:"webhook_credentials,omitempty"`
	webhookSecret           string
	webhookSecretCreatedAt  time.Time
	pendingWebhookSecret    string
	pendingSecretCreatedAt  time.Time
	previousWebhookSecret   string
	previousSecretCreatedAt time.Time
	previousSecretExpiresAt time.Time
}

type WebhookCredentialState struct {
	Active   *EncryptedWebhookSecret `json:"active,omitempty"`
	Pending  *EncryptedWebhookSecret `json:"pending,omitempty"`
	Previous *EncryptedWebhookSecret `json:"previous,omitempty"`
}

type EncryptedWebhookSecret struct {
	Version    int       `json:"version"`
	Nonce      string    `json:"nonce"`
	Ciphertext string    `json:"ciphertext"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
}

var (
	errServiceExists              = errors.New("service id already exists")
	errServiceNotFound            = errors.New("service not found")
	errWebhookSecretPending       = errors.New("pending webhook secret already exists")
	errNoPendingWebhookSecret     = errors.New("pending webhook secret not found")
	errWebhookRollbackUnavailable = errors.New("webhook secret rollback unavailable")
)

// NewInMemoryServiceStore returns an empty in-memory store.
func NewInMemoryServiceStore() *InMemoryServiceStore {
	return &InMemoryServiceStore{publicKeys: make(map[string]*rsa.PublicKey)}
}

// NewPersistentServiceStore loads services from a JSON file, or creates an
// empty file if none exists. Mutations are persisted immediately.
func NewPersistentServiceStore(path string) (*InMemoryServiceStore, error) {
	return NewPersistentServiceStoreWithEncryptionKey(path, nil)
}

func NewPersistentServiceStoreWithEncryptionKey(path string, encryptionKey []byte) (*InMemoryServiceStore, error) {
	if len(encryptionKey) != 0 && len(encryptionKey) != 32 {
		return nil, fmt.Errorf("webhook secret encryption key must be 32 bytes")
	}
	store := &InMemoryServiceStore{
		path:          path,
		publicKeys:    make(map[string]*rsa.PublicKey),
		encryptionKey: append([]byte(nil), encryptionKey...),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

// List returns a copy of all service records.
func (s *InMemoryServiceStore) List() []ServiceRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ServiceRecord, len(s.services))
	for i := range s.services {
		out[i] = cloneServiceRecord(s.services[i])
	}
	return out
}

// Get finds a service by ID.
func (s *InMemoryServiceStore) Get(id string) (*ServiceRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.services {
		if s.services[i].ID == id {
			copy := cloneServiceRecord(s.services[i])
			return &copy, true
		}
	}
	return nil, false
}

// Resolve returns the parsed RSA public key and webhook domains for a service.
func (s *InMemoryServiceStore) Resolve(id string) (*rsa.PublicKey, []string, bool) {
	s.mu.RLock()
	for i := range s.services {
		if s.services[i].ID == id {
			pub := s.publicKeys[id]
			publicKeyPEM := s.services[i].PublicKeyPEM
			domains := append([]string(nil), s.services[i].AllowedWebhookDomains...)
			s.mu.RUnlock()
			if pub != nil {
				return pub, domains, true
			}
			parsed, err := parseRSAPublicKeyPEM(publicKeyPEM)
			if err != nil {
				return nil, nil, false
			}
			s.mu.Lock()
			if s.publicKeys == nil {
				s.publicKeys = make(map[string]*rsa.PublicKey)
			}
			s.publicKeys[id] = parsed
			s.mu.Unlock()
			return parsed, domains, true
		}
	}
	s.mu.RUnlock()
	return nil, nil, false
}

// Add inserts a new service record.
func (s *InMemoryServiceStore) Add(svc ServiceRecord) {
	_ = s.Create(svc)
}

// Create inserts a service record after validating its public key.
func (s *InMemoryServiceStore) Create(svc ServiceRecord) error {
	pub, err := validateServiceRecord(svc)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.publicKeys == nil {
		s.publicKeys = make(map[string]*rsa.PublicKey)
	}
	for i := range s.services {
		if s.services[i].ID == svc.ID {
			return errServiceExists
		}
	}
	next := append([]ServiceRecord(nil), s.services...)
	next = append(next, svc)
	if err := s.saveServices(next); err != nil {
		return err
	}
	s.services = next
	s.publicKeys[svc.ID] = pub
	return nil
}

// CreateWithWebhookCredential creates a service with a new one-time webhook
// credential. The plaintext is returned only to the authenticated create call.
func (s *InMemoryServiceStore) CreateWithWebhookCredential(svc ServiceRecord) (string, error) {
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return "", fmt.Errorf("generate webhook secret: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	svc.webhookSecret = secret
	svc.webhookSecretCreatedAt = time.Now().UTC()
	if s.path != "" {
		encrypted, err := s.encryptWebhookSecret(svc.ID, "active", secret, svc.webhookSecretCreatedAt)
		if err != nil {
			return "", err
		}
		svc.WebhookCredentials = &WebhookCredentialState{Active: encrypted}
	}
	if err := s.Create(svc); err != nil {
		return "", err
	}
	return secret, nil
}

func (s *InMemoryServiceStore) ActiveWebhookSecret(serviceID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.services {
		if s.services[i].ID == serviceID && s.services[i].webhookSecret != "" {
			return s.services[i].webhookSecret, true
		}
	}
	return "", false
}

func (s *InMemoryServiceStore) RotateWebhookSecret(serviceID string, now time.Time) (string, error) {
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return "", fmt.Errorf("generate webhook secret: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.services {
		if s.services[i].ID != serviceID {
			continue
		}
		if s.services[i].pendingWebhookSecret != "" {
			return "", errWebhookSecretPending
		}
		next := append([]ServiceRecord(nil), s.services...)
		next[i].WebhookCredentials = cloneWebhookCredentialState(next[i].WebhookCredentials)
		next[i].pendingWebhookSecret = secret
		next[i].pendingSecretCreatedAt = now
		if s.path != "" {
			encrypted, err := s.encryptWebhookSecret(serviceID, "pending", secret, now)
			if err != nil {
				return "", err
			}
			if next[i].WebhookCredentials == nil {
				next[i].WebhookCredentials = &WebhookCredentialState{}
			}
			next[i].WebhookCredentials.Pending = encrypted
		}
		if err := s.saveServices(next); err != nil {
			return "", err
		}
		s.services = next
		return secret, nil
	}
	return "", errServiceNotFound
}

func (s *InMemoryServiceStore) ActivateWebhookSecret(serviceID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.services {
		current := s.services[i]
		if current.ID != serviceID {
			continue
		}
		if current.pendingWebhookSecret == "" {
			return errNoPendingWebhookSecret
		}
		next := append([]ServiceRecord(nil), s.services...)
		next[i].previousWebhookSecret = current.webhookSecret
		next[i].previousSecretCreatedAt = current.webhookSecretCreatedAt
		next[i].previousSecretExpiresAt = now.Add(10 * time.Minute)
		next[i].webhookSecret = current.pendingWebhookSecret
		next[i].webhookSecretCreatedAt = current.pendingSecretCreatedAt
		next[i].pendingWebhookSecret = ""
		next[i].pendingSecretCreatedAt = time.Time{}
		if s.path != "" {
			state := &WebhookCredentialState{}
			active, err := s.encryptWebhookSecret(serviceID, "active", next[i].webhookSecret, next[i].webhookSecretCreatedAt)
			if err != nil {
				return err
			}
			state.Active = active
			if next[i].previousWebhookSecret != "" {
				previous, err := s.encryptWebhookSecret(serviceID, "previous", next[i].previousWebhookSecret, current.webhookSecretCreatedAt)
				if err != nil {
					return err
				}
				previous.ExpiresAt = next[i].previousSecretExpiresAt
				state.Previous = previous
			}
			next[i].WebhookCredentials = state
		}
		if err := s.saveServices(next); err != nil {
			return err
		}
		s.services = next
		return nil
	}
	return errServiceNotFound
}

func (s *InMemoryServiceStore) RollbackWebhookSecret(serviceID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.services {
		current := s.services[i]
		if current.ID != serviceID {
			continue
		}
		if current.previousWebhookSecret == "" || !current.previousSecretExpiresAt.After(now) {
			return errWebhookRollbackUnavailable
		}
		next := append([]ServiceRecord(nil), s.services...)
		next[i].webhookSecret = current.previousWebhookSecret
		next[i].webhookSecretCreatedAt = current.previousSecretCreatedAt
		next[i].previousWebhookSecret = ""
		next[i].previousSecretCreatedAt = time.Time{}
		next[i].previousSecretExpiresAt = time.Time{}
		if s.path != "" {
			active, err := s.encryptWebhookSecret(serviceID, "active", next[i].webhookSecret, next[i].webhookSecretCreatedAt)
			if err != nil {
				return err
			}
			next[i].WebhookCredentials = &WebhookCredentialState{Active: active}
		}
		if err := s.saveServices(next); err != nil {
			return err
		}
		s.services = next
		return nil
	}
	return errServiceNotFound
}

func (s *InMemoryServiceStore) encryptWebhookSecret(serviceID, slot, secret string, createdAt time.Time) (*EncryptedWebhookSecret, error) {
	if len(s.encryptionKey) != 32 {
		return nil, fmt.Errorf("webhook secret encryption key is required for persistent service store")
	}
	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("create webhook secret cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create webhook secret gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate webhook secret nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(secret), webhookSecretAAD(serviceID, slot))
	return &EncryptedWebhookSecret{
		Version:    1,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
		CreatedAt:  createdAt,
	}, nil
}

func (s *InMemoryServiceStore) decryptWebhookSecret(serviceID, slot string, encrypted *EncryptedWebhookSecret) (string, error) {
	if encrypted == nil {
		return "", nil
	}
	if encrypted.Version != 1 {
		return "", fmt.Errorf("unsupported webhook secret version %d", encrypted.Version)
	}
	if len(s.encryptionKey) != 32 {
		return "", fmt.Errorf("webhook secret encryption key is required")
	}
	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce, err := base64.StdEncoding.DecodeString(encrypted.Nonce)
	if err != nil {
		return "", fmt.Errorf("decode webhook secret nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encrypted.Ciphertext)
	if err != nil {
		return "", fmt.Errorf("decode webhook secret ciphertext: %w", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, webhookSecretAAD(serviceID, slot))
	if err != nil {
		return "", fmt.Errorf("decrypt webhook secret: %w", err)
	}
	return string(plaintext), nil
}

func webhookSecretAAD(serviceID, slot string) []byte {
	return []byte("onlyoffice-gateway:webhook-secret:v1:" + serviceID + ":" + slot)
}

// Remove deletes a service by ID and returns true if it existed.
func (s *InMemoryServiceStore) Remove(id string) bool {
	return s.Delete(id) == nil
}

// Delete removes a service record and persists the new registry.
func (s *InMemoryServiceStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.services {
		if s.services[i].ID == id {
			next := append([]ServiceRecord(nil), s.services[:i]...)
			next = append(next, s.services[i+1:]...)
			if err := s.saveServices(next); err != nil {
				return err
			}
			s.services = next
			delete(s.publicKeys, id)
			return nil
		}
	}
	return errServiceNotFound
}

// Update replaces all fields of an existing service. Returns an error if the
// service does not exist.
func (s *InMemoryServiceStore) Update(id string, svc ServiceRecord) error {
	pub, err := validateServiceRecord(svc)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.publicKeys == nil {
		s.publicKeys = make(map[string]*rsa.PublicKey)
	}
	for i := range s.services {
		if s.services[i].ID == id {
			svc.WebhookCredentials = cloneWebhookCredentialState(s.services[i].WebhookCredentials)
			svc.webhookSecret = s.services[i].webhookSecret
			svc.webhookSecretCreatedAt = s.services[i].webhookSecretCreatedAt
			svc.pendingWebhookSecret = s.services[i].pendingWebhookSecret
			svc.pendingSecretCreatedAt = s.services[i].pendingSecretCreatedAt
			svc.previousWebhookSecret = s.services[i].previousWebhookSecret
			svc.previousSecretCreatedAt = s.services[i].previousSecretCreatedAt
			svc.previousSecretExpiresAt = s.services[i].previousSecretExpiresAt
			next := append([]ServiceRecord(nil), s.services...)
			next[i] = svc
			next[i].ID = id
			if err := s.saveServices(next); err != nil {
				return err
			}
			s.services = next
			s.publicKeys[id] = pub
			return nil
		}
	}
	return errServiceNotFound
}

func cloneServiceRecord(svc ServiceRecord) ServiceRecord {
	svc.AllowedWebhookDomains = append([]string(nil), svc.AllowedWebhookDomains...)
	svc.WebhookCredentials = cloneWebhookCredentialState(svc.WebhookCredentials)
	return svc
}

func cloneWebhookCredentialState(state *WebhookCredentialState) *WebhookCredentialState {
	if state == nil {
		return nil
	}
	clone := &WebhookCredentialState{}
	if state.Active != nil {
		active := *state.Active
		clone.Active = &active
	}
	if state.Pending != nil {
		pending := *state.Pending
		clone.Pending = &pending
	}
	if state.Previous != nil {
		previous := *state.Previous
		clone.Previous = &previous
	}
	return clone
}

func (s *InMemoryServiceStore) load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return os.WriteFile(s.path, []byte("[]"), 0600)
	}
	if err != nil {
		return fmt.Errorf("read services file: %w", err)
	}
	if err := json.Unmarshal(data, &s.services); err != nil {
		return err
	}
	for i := range s.services {
		svc := &s.services[i]
		pub, err := validateServiceRecord(*svc)
		if err != nil {
			return fmt.Errorf("service %s: %w", svc.ID, err)
		}
		s.publicKeys[svc.ID] = pub
		if svc.WebhookCredentials != nil && svc.WebhookCredentials.Active != nil {
			secret, err := s.decryptWebhookSecret(svc.ID, "active", svc.WebhookCredentials.Active)
			if err != nil {
				return fmt.Errorf("service %s active webhook secret: %w", svc.ID, err)
			}
			svc.webhookSecret = secret
			svc.webhookSecretCreatedAt = svc.WebhookCredentials.Active.CreatedAt
		}
		if svc.WebhookCredentials != nil && svc.WebhookCredentials.Pending != nil {
			secret, err := s.decryptWebhookSecret(svc.ID, "pending", svc.WebhookCredentials.Pending)
			if err != nil {
				return fmt.Errorf("service %s pending webhook secret: %w", svc.ID, err)
			}
			svc.pendingWebhookSecret = secret
			svc.pendingSecretCreatedAt = svc.WebhookCredentials.Pending.CreatedAt
		}
		if svc.WebhookCredentials != nil && svc.WebhookCredentials.Previous != nil {
			secret, err := s.decryptWebhookSecret(svc.ID, "previous", svc.WebhookCredentials.Previous)
			if err != nil {
				return fmt.Errorf("service %s previous webhook secret: %w", svc.ID, err)
			}
			svc.previousWebhookSecret = secret
			svc.previousSecretCreatedAt = svc.WebhookCredentials.Previous.CreatedAt
			svc.previousSecretExpiresAt = svc.WebhookCredentials.Previous.ExpiresAt
		}
	}
	return nil
}

func (s *InMemoryServiceStore) save() {
	_ = s.saveServices(s.services)
}

func (s *InMemoryServiceStore) saveServices(services []ServiceRecord) error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(services, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal services: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".services-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp services file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp services file: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp services file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temp services file: %w", err)
	}
	if err = os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace services file: %w", err)
	}
	return nil
}

func validateServiceRecord(svc ServiceRecord) (*rsa.PublicKey, error) {
	if svc.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	if strings.TrimSpace(svc.PublicKeyPEM) == "" {
		return nil, fmt.Errorf("public_key is required")
	}
	pub, err := parseRSAPublicKeyPEM(svc.PublicKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("invalid public_key: %w", err)
	}
	return pub, nil
}

func parseRSAPublicKeyPEM(pemData string) (*rsa.PublicKey, error) {
	lines := strings.Split(pemData, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	cleaned := strings.Join(lines, "\n")
	block, _ := pem.Decode([]byte(cleaned))
	if block == nil {
		return nil, fmt.Errorf("invalid PEM: not a valid PEM block")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("key is not an RSA public key")
	}
	return rsaPub, nil
}
