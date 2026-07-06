package config

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type ServiceConfig struct {
	ID                    string   `yaml:"id"`
	PublicKeyPEM          string   `yaml:"public_key"`
	PublicKeyFile         string   `yaml:"public_key_file"`
	AllowedWebhookDomains []string `yaml:"allowed_webhook_domains"`
	parsedPublicKey       *rsa.PublicKey
}

type Config struct {
	ListenAddr        string          `yaml:"listen_addr"`
	DocumentServerURL string          `yaml:"document_server_url"`
	JWTSecret         string          `yaml:"jwt_secret"`
	StorageDir        string          `yaml:"storage_dir"`
	TTLHours          int             `yaml:"ttl_hours"`
	WebhookMaxRetries int             `yaml:"webhook_max_retries"`
	Services          []ServiceConfig `yaml:"services"`
}

func Defaults() *Config {
	return &Config{
		ListenAddr:        ":18080",
		StorageDir:        "./data/storage",
		TTLHours:          8,
		WebhookMaxRetries: 3,
	}
}

func Load(path string) (*Config, error) {
	cfg := Defaults()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	for i := range cfg.Services {
		if err := cfg.Services[i].resolvePublicKey(); err != nil {
			return nil, fmt.Errorf("service %s: %w", cfg.Services[i].ID, err)
		}
	}
	// Override from environment variables
	if s := os.Getenv("JWT_SECRET"); s != "" {
		cfg.JWTSecret = s
	}
	cfg.StorageDir, err = filepath.Abs(cfg.StorageDir)
	if err != nil {
		return nil, fmt.Errorf("resolve storage dir: %w", err)
	}
	return cfg, nil
}

func (c *Config) GetService(id string) (*ServiceConfig, error) {
	for i := range c.Services {
		if c.Services[i].ID == id {
			return &c.Services[i], nil
		}
	}
	return nil, fmt.Errorf("service %s not found", id)
}

func (s *ServiceConfig) PublicKey() *rsa.PublicKey {
	return s.parsedPublicKey
}

func (s *ServiceConfig) IsWebhookDomainAllowed(rawURL string) bool {
	if len(s.AllowedWebhookDomains) == 0 {
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
	for _, d := range s.AllowedWebhookDomains {
		if host == d {
			return true
		}
	}
	return false
}

// FromLiteral takes a pre-populated Config, processes public keys and paths,
// and returns a ready-to-use configuration. Useful for testing.
func FromLiteral(cfg *Config) (*Config, error) {
	for i := range cfg.Services {
		if err := cfg.Services[i].resolvePublicKey(); err != nil {
			return nil, fmt.Errorf("service %s: %w", cfg.Services[i].ID, err)
		}
	}
	var err error
	if s := os.Getenv("JWT_SECRET"); s != "" {
		cfg.JWTSecret = s
	}
	cfg.StorageDir, err = filepath.Abs(cfg.StorageDir)
	if err != nil {
		return nil, fmt.Errorf("resolve storage dir: %w", err)
	}
	return cfg, nil
}

// SetPublicKey sets the parsed RSA public key.
func (s *ServiceConfig) SetPublicKey(key *rsa.PublicKey) {
	s.parsedPublicKey = key
}

// resolvePublicKey loads the public key from file or normalizes the inline PEM.
func (s *ServiceConfig) resolvePublicKey() error {
	raw := s.PublicKeyPEM

	// public_key_file takes precedence — a plain path, zero YAML indentation risk
	if s.PublicKeyFile != "" {
		data, err := os.ReadFile(s.PublicKeyFile)
		if err != nil {
			return fmt.Errorf("read public_key_file %s: %w", s.PublicKeyFile, err)
		}
		raw = string(data)
	}

	if strings.TrimSpace(raw) == "" {
		return nil
	}

	pub, err := parseRSAPublicKeyPEM(raw)
	if err != nil {
		return err
	}
	s.parsedPublicKey = pub
	return nil
}

// parseRSAPublicKeyPEM parses a PEM-encoded RSA public key string.
// Leading/trailing whitespace on each line is stripped to tolerate
// common YAML indentation mistakes.
func parseRSAPublicKeyPEM(pemData string) (*rsa.PublicKey, error) {
	// Strip leading/trailing whitespace from each line, then reassemble.
	// This handles cases where YAML indentation is inconsistent across lines.
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
