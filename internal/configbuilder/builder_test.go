package configbuilder_test

import (
	"encoding/json"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/zenmind/onlyoffice-gateway/internal/configbuilder"
)

func unmarshal(v interface{}) map[string]interface{} {
	var m map[string]interface{}
	b, _ := json.Marshal(v)
	json.Unmarshal(b, &m)
	return m
}

// S21: Builder without overrides returns base ONLYOFFICE config.
func TestBuilderReturnsGatewayDefaults(t *testing.T) {
	builder := configbuilder.New(configbuilder.Params{
		DocumentServerURL: "https://doc.example.com",
		CallbackURL:       "https://gateway.example.com/callback",
		DownloadURL:       "https://gateway.example.com/download/doc-123",
		FileType:          "docx",
		Key:               "Khirz6zTPdfd7",
		Title:             "test.docx",
		DocumentType:      "word",
	})

	result := builder.Build()
	m := unmarshal(result)

	// Layer 1: Gateway defaults
	doc := m["document"].(map[string]interface{})
	if doc["fileType"] != "docx" {
		t.Fatalf("expected fileType docx, got %v", doc["fileType"])
	}
	if doc["key"] != "Khirz6zTPdfd7" {
		t.Fatalf("expected key, got %v", doc["key"])
	}
	if doc["url"] != "https://gateway.example.com/download/doc-123" {
		t.Fatalf("expected url, got %v", doc["url"])
	}

	ec := m["editorConfig"].(map[string]interface{})
	if ec["callbackUrl"] != "https://gateway.example.com/callback" {
		t.Fatalf("expected callbackUrl, got %v", ec["callbackUrl"])
	}
}

// S22: Branding fields map to ONLYOFFICE customization config.
func TestBuilderMergesBranding(t *testing.T) {
	builder := configbuilder.New(configbuilder.Params{
		DocumentServerURL: "https://doc.example.com",
		CallbackURL:       "https://gateway.example.com/callback",
		DownloadURL:       "https://gateway.example.com/download/doc-123",
		FileType:          "docx",
		Key:               "key-1",
		Title:             "branded.docx",
		DocumentType:      "word",
		// Layer 2: limited customization via branding
		Branding: &configbuilder.Branding{
			LogoURL:    "https://myapp.com/logo.png",
			ColorTheme: "#ff6600",
			Language:   "zh-CN",
		},
	})

	result := builder.Build()
	m := unmarshal(result)

	ec := m["editorConfig"].(map[string]interface{})
	cust := ec["customization"].(map[string]interface{})
	logo := cust["logo"].(map[string]interface{})
	if logo["image"] != "https://myapp.com/logo.png" {
		t.Fatalf("expected logo, got %v", logo["image"])
	}

	if m["editorConfig"].(map[string]interface{})["lang"] != "zh-CN" {
		t.Fatalf("expected lang zh-CN")
	}
}

// S23: config_overrides win over branding (Layer 3 > Layer 2).
func TestBuilderOverridesWithConfigOverrides(t *testing.T) {
	builder := configbuilder.New(configbuilder.Params{
		DocumentServerURL: "https://doc.example.com",
		CallbackURL:       "https://gateway.example.com/callback",
		DownloadURL:       "https://gateway.example.com/download/doc-123",
		FileType:          "docx",
		Key:               "key-2",
		Title:             "override.docx",
		DocumentType:      "word",
		Branding: &configbuilder.Branding{
			LogoURL:  "https://myapp.com/logo.png",
			Language: "zh-CN",
		},
		// Layer 3: full override wins
		ConfigOverrides: map[string]interface{}{
			"editorConfig": map[string]interface{}{
				"lang": "en-US",
				"customization": map[string]interface{}{
					"compactToolbar": true,
				},
			},
			"permissions": map[string]interface{}{
				"comment": false,
			},
		},
	})

	result := builder.Build()
	m := unmarshal(result)

	// Language should be whatever config_overrides says (Layer 3 wins)
	if m["editorConfig"].(map[string]interface{})["lang"] != "en-US" {
		t.Fatalf("expected lang en-US from override")
	}

	// Branding logo should still be present (only lang was overridden, not logo)
	ec := m["editorConfig"].(map[string]interface{})
	cust := ec["customization"].(map[string]interface{})
	if cust["compactToolbar"] != true {
		t.Fatalf("expected compactToolbar from override")
	}
	logo := cust["logo"].(map[string]interface{})
	if logo["image"] != "https://myapp.com/logo.png" {
		t.Fatalf("expected logo preserved from branding")
	}

	// permissions from override should be present
	if m["permissions"].(map[string]interface{})["comment"] != false {
		t.Fatalf("expected comment: false from override")
	}
}

func TestBuilderViewModeDisablesEditing(t *testing.T) {
	builder := configbuilder.New(configbuilder.Params{
		DocumentServerURL: "https://doc.example.com",
		CallbackURL:       "https://gateway.example.com/callback",
		DownloadURL:       "https://gateway.example.com/download/doc-123",
		FileType:          "docx",
		Key:               "key-view",
		Title:             "readonly.docx",
		DocumentType:      "word",
		Mode:              "view",
	})

	result := builder.Build()
	m := unmarshal(result)

	if m["mode"] != "view" {
		t.Fatalf("expected top-level mode view, got %v", m["mode"])
	}
	permissions := m["document"].(map[string]interface{})["permissions"].(map[string]interface{})
	if permissions["edit"] != false {
		t.Fatalf("expected edit permission false in view mode, got %v", permissions["edit"])
	}
	if permissions["download"] != true {
		t.Fatalf("expected download permission true in view mode, got %v", permissions["download"])
	}
}

func TestBuilderSignsConfigWhenJWTSecretProvided(t *testing.T) {
	builder := configbuilder.New(configbuilder.Params{
		DocumentServerURL: "https://doc.example.com",
		CallbackURL:       "https://gateway.example.com/callback",
		DownloadURL:       "https://gateway.example.com/download/doc-123",
		FileType:          "docx",
		Key:               "key-signed",
		Title:             "signed.docx",
		DocumentType:      "word",
		JWTSecret:         "shared-secret",
	})

	result := builder.Build()
	m := unmarshal(result)
	tokenString, ok := m["token"].(string)
	if !ok || tokenString == "" {
		t.Fatal("expected signed ONLYOFFICE config token")
	}

	token, err := jwt.Parse(tokenString, func(parsed *jwt.Token) (interface{}, error) {
		if parsed.Method != jwt.SigningMethodHS256 {
			t.Fatalf("expected HS256 token, got %v", parsed.Header["alg"])
		}
		return []byte("shared-secret"), nil
	})
	if err != nil || !token.Valid {
		t.Fatalf("expected valid config token, got token=%v err=%v", token.Valid, err)
	}

	claims := token.Claims.(jwt.MapClaims)
	document := claims["document"].(map[string]interface{})
	if document["url"] != "https://gateway.example.com/download/doc-123" {
		t.Fatalf("expected document url in signed token, got %v", document["url"])
	}
}
