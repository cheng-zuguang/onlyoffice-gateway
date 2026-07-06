package admin

import (
	"crypto/rsa"
	"crypto/subtle"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Opts holds dependencies for the admin mux.
type Opts struct {
	AdminUsername string
	AdminPassword string
	JWTSecret     string
	Store         *InMemoryServiceStore
}

// NewMux returns an http.Handler mounting all admin API routes.
func NewMux(opts Opts) http.Handler {
	mux := http.NewServeMux()
	auth := &authHandler{
		username: opts.AdminUsername,
		password: opts.AdminPassword,
		jwtKey:   []byte(opts.JWTSecret),
	}
	mux.HandleFunc("POST /admin/api/login", auth.handleLogin)

	svc := &serviceHandler{store: opts.Store}
	protect := func(h http.HandlerFunc) http.HandlerFunc {
		return auth.middleware(h)
	}
	mux.HandleFunc("GET /admin/api/services", protect(svc.handleListServices))
	mux.HandleFunc("POST /admin/api/services", protect(svc.handleCreateService))
	mux.HandleFunc("PUT /admin/api/services/{id}", protect(svc.handleUpdateService))
	mux.HandleFunc("DELETE /admin/api/services/{id}", protect(svc.handleDeleteService))

	return mux
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
}

func (h *serviceHandler) handleListServices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.store.List())
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
	if _, ok := h.store.Get(svc.ID); ok {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "service id already exists"})
		return
	}
	h.store.Add(svc)
	writeJSON(w, http.StatusCreated, svc)
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
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	updated, _ := h.store.Get(id)
	writeJSON(w, http.StatusOK, updated)
}

func (h *serviceHandler) handleDeleteService(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !h.store.Remove(id) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "service not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
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
	mu       sync.RWMutex
	services []ServiceRecord
	path     string
}

// ServiceRecord represents one external service authorized to use the gateway.
type ServiceRecord struct {
	ID                    string   `json:"id"`
	PublicKeyPEM          string   `json:"public_key"`
	AllowedWebhookDomains []string `json:"allowed_webhook_domains"`
}

// NewInMemoryServiceStore returns an empty in-memory store.
func NewInMemoryServiceStore() *InMemoryServiceStore {
	return &InMemoryServiceStore{}
}

// NewPersistentServiceStore loads services from a JSON file, or creates an
// empty file if none exists. Mutations are persisted immediately.
func NewPersistentServiceStore(path string) (*InMemoryServiceStore, error) {
	store := &InMemoryServiceStore{path: path}
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
	copy(out, s.services)
	return out
}

// Get finds a service by ID.
func (s *InMemoryServiceStore) Get(id string) (*ServiceRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.services {
		if s.services[i].ID == id {
			return &s.services[i], true
		}
	}
	return nil, false
}

// Resolve returns the parsed RSA public key and webhook domains for a service.
func (s *InMemoryServiceStore) Resolve(id string) (*rsa.PublicKey, []string, bool) {
	svc, ok := s.Get(id)
	if !ok {
		return nil, nil, false
	}
	pub, err := parseRSAPublicKeyPEM(svc.PublicKeyPEM)
	if err != nil {
		return nil, nil, false
	}
	return pub, svc.AllowedWebhookDomains, true
}

// Add inserts a new service record.
func (s *InMemoryServiceStore) Add(svc ServiceRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.services = append(s.services, svc)
	s.save()
}

// Remove deletes a service by ID and returns true if it existed.
func (s *InMemoryServiceStore) Remove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.services {
		if s.services[i].ID == id {
			s.services = append(s.services[:i], s.services[i+1:]...)
			s.save()
			return true
		}
	}
	return false
}

// Update replaces all fields of an existing service. Returns an error if the
// service does not exist.
func (s *InMemoryServiceStore) Update(id string, svc ServiceRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.services {
		if s.services[i].ID == id {
			s.services[i] = svc
			s.services[i].ID = id
			s.save()
			return nil
		}
	}
	return fmt.Errorf("service %s not found", id)
}

func (s *InMemoryServiceStore) load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return os.WriteFile(s.path, []byte("[]"), 0644)
	}
	if err != nil {
		return fmt.Errorf("read services file: %w", err)
	}
	return json.Unmarshal(data, &s.services)
}

func (s *InMemoryServiceStore) save() {
	if s.path == "" {
		return
	}
	data, _ := json.MarshalIndent(s.services, "", "  ")
	_ = os.WriteFile(s.path, data, 0644)
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
