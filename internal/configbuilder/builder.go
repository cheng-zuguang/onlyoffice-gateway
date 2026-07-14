package configbuilder

import (
	"encoding/json"

	"github.com/golang-jwt/jwt/v5"
)

// Branding defines limited customization options for the editor.
type Branding struct {
	LogoURL    string `json:"logo_url,omitempty"`
	ColorTheme string `json:"color_theme,omitempty"`
	Language   string `json:"language,omitempty"`
}

// Params holds all inputs for building an ONLYOFFICE config.
type Params struct {
	DocumentServerURL string
	CallbackURL       string
	DownloadURL       string
	FileType          string
	Key               string
	Title             string
	DocumentType      string
	Mode              string
	User              map[string]interface{}
	Branding          *Branding
	ConfigOverrides   map[string]interface{}
	JWTSecret         string
}

// Config is the output ONLYOFFICE configuration.
type Config map[string]interface{}

// Builder constructs ONLYOFFICE editor config with layered merge.
type Builder struct {
	params Params
}

func New(p Params) *Builder {
	return &Builder{params: p}
}

func (b *Builder) Build() json.RawMessage {
	// Layer 1: Gateway defaults
	cfg := Config{
		"document": map[string]interface{}{
			"fileType": b.params.FileType,
			"key":      b.params.Key,
			"title":    b.params.Title,
			"url":      b.params.DownloadURL,
		},
		"documentType": onlyOfficeDocumentType(b.params.DocumentType),
		"editorConfig": map[string]interface{}{
			"callbackUrl": b.params.CallbackURL,
			"user":        b.params.User,
		},
	}

	// Layer 2: Branding → ONLYOFFICE customization
	if b.params.Branding != nil {
		ec := cfg["editorConfig"].(map[string]interface{})
		cust := make(map[string]interface{})
		if b.params.Branding.LogoURL != "" {
			cust["logo"] = map[string]interface{}{
				"image": b.params.Branding.LogoURL,
			}
		}
		ec["customization"] = cust
		if b.params.Branding.Language != "" {
			ec["lang"] = b.params.Branding.Language
		}
	}

	// Layer 3: Config overrides (deep merge, wins over layer 1+2)
	if b.params.ConfigOverrides != nil {
		deepMerge(cfg, b.params.ConfigOverrides)
	}

	if b.params.Mode == "view" {
		ec := cfg["editorConfig"].(map[string]interface{})
		ec["mode"] = "view"
		doc := cfg["document"].(map[string]interface{})
		permissions, _ := doc["permissions"].(map[string]interface{})
		if permissions == nil {
			permissions = map[string]interface{}{}
			doc["permissions"] = permissions
		}
		permissions["edit"] = false
		permissions["download"] = true
	}

	if b.params.JWTSecret != "" {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(cfg))
		if signed, err := token.SignedString([]byte(b.params.JWTSecret)); err == nil {
			cfg["token"] = signed
		}
	}

	data, _ := json.Marshal(cfg)
	return data
}

func onlyOfficeDocumentType(documentType string) string {
	if documentType == "pdf" {
		return "word"
	}
	return documentType
}

// deepMerge recursively merges src into dst. Src wins on conflicts.
func deepMerge(dst, src map[string]interface{}) {
	for key, srcVal := range src {
		if dstVal, ok := dst[key]; ok {
			dstMap, dstOk := dstVal.(map[string]interface{})
			srcMap, srcOk := srcVal.(map[string]interface{})
			if dstOk && srcOk {
				deepMerge(dstMap, srcMap)
				continue
			}
		}
		dst[key] = srcVal
	}
}
