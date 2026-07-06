package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr        string `yaml:"listen_addr"`
	DocumentServerURL string `yaml:"document_server_url"`
	JWTSecret         string `yaml:"jwt_secret"`
	StorageDir        string `yaml:"storage_dir"`
	TTLHours          int    `yaml:"ttl_hours"`
	WebhookMaxRetries int    `yaml:"webhook_max_retries"`
}

func Defaults() *Config {
	return &Config{
		ListenAddr:        ":18080",
		StorageDir:        "./data/storage",
		TTLHours:          8,
		WebhookMaxRetries: 3,
	}
}

// Load reads config from a YAML file and overrides with environment variables.
// Services are no longer loaded from YAML — use the admin API to manage them.
func Load(path string) (*Config, error) {
	cfg := Defaults()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// No config file — use defaults + env overrides
			return applyEnvOverrides(cfg)
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return applyEnvOverrides(cfg)
}

func applyEnvOverrides(cfg *Config) (*Config, error) {
	if s := os.Getenv("JWT_SECRET"); s != "" {
		cfg.JWTSecret = s
	}
	if s := os.Getenv("LISTEN_ADDR"); s != "" {
		cfg.ListenAddr = s
	}
	if s := os.Getenv("DOCUMENT_SERVER_URL"); s != "" {
		cfg.DocumentServerURL = s
	}
	if s := os.Getenv("STORAGE_DIR"); s != "" {
		cfg.StorageDir = s
	}
	if s := os.Getenv("TTL_HOURS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			cfg.TTLHours = v
		}
	}
	if s := os.Getenv("WEBHOOK_MAX_RETRIES"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			cfg.WebhookMaxRetries = v
		}
	}

	var err error
	cfg.StorageDir, err = filepath.Abs(cfg.StorageDir)
	if err != nil {
		return nil, fmt.Errorf("resolve storage dir: %w", err)
	}
	return cfg, nil
}

// FromLiteral applies env overrides and resolves paths for a pre-populated
// Config (useful for testing).
func FromLiteral(cfg *Config) (*Config, error) {
	return applyEnvOverrides(cfg)
}
