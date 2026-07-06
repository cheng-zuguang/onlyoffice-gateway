package config

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type ServiceConfig struct {
	ID                    string   `yaml:"id"`
	PublicKeyPEM          string   `yaml:"public_key"`
	AllowedWebhookDomains []string `yaml:"allowed_webhook_domains"`
	parsedPublicKey       *rsa.PublicKey
}

type Config struct {
	ListenAddr         string        `yaml:"listen_addr"`
	DocumentServerURL  string        `yaml:"document_server_url"`
	JWTSecret          string        `yaml:"jwt_secret"`
	StorageDir         string        `yaml:"storage_dir"`
	TTLHours           int           `yaml:"ttl_hours"`
	WebhookMaxRetries  int           `yaml:"webhook_max_retries"`
	Services           []ServiceConfig `yaml:"services"`
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
		if cfg.Services[i].PublicKeyPEM == "" {
			continue
		}
		block, _ := pem.Decode([]byte(cfg.Services[i].PublicKeyPEM))
		if block == nil {
			return nil, fmt.Errorf("service %s: invalid PEM", cfg.Services[i].ID)
		}
		pub, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("service %s: parse public key: %w", cfg.Services[i].ID, err)
		}
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("service %s: key is not RSA public key", cfg.Services[i].ID)
		}
		cfg.Services[i].parsedPublicKey = rsaPub
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
	// Simple domain match
	fromURL := rawURL
	// Strip scheme
	if len(fromURL) > 8 && fromURL[:8] == "https://" {
		fromURL = fromURL[8:]
	} else if len(fromURL) > 7 && fromURL[:7] == "http://" {
		fromURL = fromURL[7:]
	}
	// Get host part
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
		if cfg.Services[i].PublicKeyPEM == "" {
			continue
		}
		block, _ := pem.Decode([]byte(cfg.Services[i].PublicKeyPEM))
		if block == nil {
			return nil, fmt.Errorf("service %s: invalid PEM", cfg.Services[i].ID)
		}
		pub, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("service %s: parse public key: %w", cfg.Services[i].ID, err)
		}
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("service %s: key is not RSA public key", cfg.Services[i].ID)
		}
		cfg.Services[i].SetPublicKey(rsaPub)
	}
	var err error
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

// SetPublicKey sets the parsed RSA public key.
func (s *ServiceConfig) SetPublicKey(key *rsa.PublicKey) {
	s.parsedPublicKey = key
}
