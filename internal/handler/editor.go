package handler

import (
	"fmt"
	"html"
	"net/http"

	"github.com/zenmind/onlyoffice-gateway/internal/config"
	"github.com/zenmind/onlyoffice-gateway/internal/configbuilder"
	gwjwt "github.com/zenmind/onlyoffice-gateway/internal/jwt"
	"github.com/zenmind/onlyoffice-gateway/internal/storage"
)

type EditorHandler struct {
	cfg       *config.Config
	resolver  gwjwt.ServiceResolver
	store     storage.Store
	serverURL string
}

func NewEditorHandler(cfg *config.Config, resolver gwjwt.ServiceResolver, store storage.Store, serverURL string) *EditorHandler {
	return &EditorHandler{cfg: cfg, resolver: resolver, store: store, serverURL: serverURL}
}

func (h *EditorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tokenStr := r.URL.Query().Get("token")
	mode := r.URL.Query().Get("mode")
	if mode != "view" {
		mode = ""
	}

	if tokenStr == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing token"})
		return
	}

	claims, err := gwjwt.VerifyServiceJWT(h.resolver, tokenStr)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
		return
	}
	documentID, _ := claims["document_id"].(string)
	if documentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing document_id in token"})
		return
	}

	meta, err := h.store.GetMeta(r.Context(), documentID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "document not found"})
		return
	}

	// Build branding from meta
	var branding *configbuilder.Branding
	if meta.Branding != nil {
		if b, ok := meta.Branding.(map[string]interface{}); ok {
			logoURL, _ := b["logo_url"].(string)
			lang, _ := b["language"].(string)
			color, _ := b["color_theme"].(string)
			branding = &configbuilder.Branding{
				LogoURL:    logoURL,
				Language:   lang,
				ColorTheme: color,
			}
		}
	}

	// Build config overrides from meta
	var overrides map[string]interface{}
	if meta.ConfigOverrides != nil {
		overrides, _ = meta.ConfigOverrides.(map[string]interface{})
	}

	downloadURL := h.serverURL + "/download/" + documentID
	if meta.SourceURL != "" {
		downloadURL = meta.SourceURL
	}

	// Build the ONLYOFFICE config using the config builder
	builder := configbuilder.New(configbuilder.Params{
		DocumentServerURL: h.cfg.DocumentServerURL,
		CallbackURL:       h.serverURL + "/callback?token=" + callbackCapability(documentID, h.cfg.JWTSecret),
		DownloadURL:       downloadURL,
		FileType:          meta.FileType,
		Key:               documentID,
		Title:             meta.FileName,
		DocumentType:      meta.DocumentType,
		Mode:              mode,
		Branding:          branding,
		ConfigOverrides:   overrides,
		User: map[string]interface{}{
			"id":   "gateway-user",
			"name": "User",
		},
		JWTSecret: h.cfg.JWTSecret,
	})

	configJSON := builder.Build()
	documentServerBrowserURL := h.cfg.DocumentServerPublicURL
	if documentServerBrowserURL == "" {
		documentServerBrowserURL = h.serverURL
	}

	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"><title>%s</title></head>
<body style="margin:0;padding:0;height:100vh;">
<div id="placeholder" style="width:100%%;height:100%%;"></div>
<script src="%s/web-apps/apps/api/documents/api.js"></script>
<script>
(function() {
  var config = %s;

  function post(type, data) {
    window.parent.postMessage(JSON.stringify({type: "onlyoffice:" + type, data: data || {}}), "*");
  }

  var docEditor = new DocsAPI.DocEditor("placeholder", config);

  docEditor.addEventListener("onAppReady", function() {
    post("ready");
  });

  docEditor.addEventListener("onDocumentStateChange", function(e) {
    if (e.data) post("saved", e.data);
  });

  docEditor.addEventListener("onError", function(e) {
    post("error", e.data);
  });
})();
</script>
</body>
</html>`,
		html.EscapeString(meta.FileName),
		documentServerBrowserURL,
		string(configJSON),
	)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Write([]byte(html))
}
