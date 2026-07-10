package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/zenmind/onlyoffice-gateway/internal/config"
	gwjwt "github.com/zenmind/onlyoffice-gateway/internal/jwt"
	"github.com/zenmind/onlyoffice-gateway/internal/storage"
)

type UploadHandler struct {
	cfg      *config.Config
	resolver gwjwt.ServiceResolver
	store    storage.Store
}

func NewUploadHandler(cfg *config.Config, resolver gwjwt.ServiceResolver, store storage.Store) *UploadHandler {
	return &UploadHandler{cfg: cfg, resolver: resolver, store: store}
}

func (h *UploadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// Extract and verify JWT
	token := gwjwt.ExtractBearer(r.Header.Get("Authorization"))
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing authorization"})
		return
	}

	claims, err := gwjwt.VerifyServiceJWT(h.resolver, token)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	// Parse claims
	serviceID, _ := claims["service_id"].(string)
	webhookURL, _ := claims["webhook_url"].(string)
	externalID, _ := claims["external_id"].(string)
	fileName, _ := claims["file_name"].(string)
	docType, _ := claims["document_type"].(string)

	// Validate webhook domain
	if serviceID != "" {
		_, domains, found := h.resolver.Resolve(serviceID)
		if found && !isDomainAllowed(domains, webhookURL) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "webhook domain not allowed"})
			return
		}
	}

	// Read uploaded file
	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file"})
		return
	}
	defer file.Close()

	// Generate document ID and editor key
	documentID := generateDocID()
	editorKey := generateEditorKey()
	now := time.Now()

	meta := storage.Meta{
		Branding:        claims["branding"],
		ConfigOverrides: claims["config_overrides"],
		DocumentID:      documentID,
		ServiceID:       serviceID,
		ExternalID:      externalID,
		WebhookURL:      webhookURL,
		FileName:        fileName,
		FileType:        docTypeToFileType(docType),
		DocumentType:    docType,
		EditorKey:       editorKey,
		CreatedAt:       now,
		ExpiresAt:       now.Add(time.Duration(h.cfg.TTLHours) * time.Hour),
	}

	if err := h.store.Put(r.Context(), documentID, file, meta); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "storage error"})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"document_id": documentID,
		"status":      "uploaded",
		"expires_at":  meta.ExpiresAt.Format(time.RFC3339),
	})
}

func isDomainAllowed(allowedDomains []string, rawURL string) bool {
	if len(allowedDomains) == 0 {
		return false
	}
	fromURL := rawURL
	if len(fromURL) > 8 && fromURL[:8] == "https://" {
		fromURL = fromURL[8:]
	} else if len(fromURL) > 7 && fromURL[:7] == "http://" {
		fromURL = fromURL[7:]
	}
	host := fromURL
	for i, c := range fromURL {
		if c == '/' || c == ':' || c == '?' {
			host = fromURL[:i]
			break
		}
	}
	for _, d := range allowedDomains {
		if host == d {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func generateDocID() string {
	return "doc_" + time.Now().Format("20060102") + randomString(8)
}

func generateEditorKey() string {
	return randomString(16)
}

func docTypeToFileType(docType string) string {
	switch docType {
	case "word":
		return "docx"
	case "cell":
		return "xlsx"
	case "slide":
		return "pptx"
	default:
		return "pdf"
	}
}
