package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr              string `yaml:"listen_addr"`
	DocumentServerURL       string `yaml:"document_server_url"`
	DocumentServerPublicURL string `yaml:"document_server_public_url"`
	JWTSecret               string `yaml:"jwt_secret"`
	StorageBackend          string `yaml:"storage_backend"`
	StorageDir              string `yaml:"storage_dir"`
	S3Endpoint              string `yaml:"s3_endpoint"`
	S3Region                string `yaml:"s3_region"`
	S3Bucket                string `yaml:"s3_bucket"`
	S3AccessKey             string `yaml:"s3_access_key"`
	S3SecretKey             string `yaml:"s3_secret_key"`
	S3UsePathStyle          bool   `yaml:"s3_use_path_style"`
	S3UseSSL                bool   `yaml:"s3_use_ssl"`
	S3Prefix                string `yaml:"s3_prefix"`
	TTLHours                int    `yaml:"ttl_hours"`
	WebhookMaxRetries       int    `yaml:"webhook_max_retries"`
}

func Defaults() *Config {
	return &Config{
		ListenAddr:        ":18080",
		StorageBackend:    "local",
		StorageDir:        "./data/storage",
		S3Region:          "us-east-1",
		S3UsePathStyle:    true,
		S3UseSSL:          true,
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
	if s := os.Getenv("DOCUMENT_SERVER_PUBLIC_URL"); s != "" {
		cfg.DocumentServerPublicURL = s
	}
	if s := os.Getenv("STORAGE_BACKEND"); s != "" {
		cfg.StorageBackend = s
	}
	if s := os.Getenv("STORAGE_DIR"); s != "" {
		cfg.StorageDir = s
	}
	if s := os.Getenv("S3_ENDPOINT"); s != "" {
		cfg.S3Endpoint = s
	}
	if s := os.Getenv("S3_REGION"); s != "" {
		cfg.S3Region = s
	}
	if s := os.Getenv("S3_BUCKET"); s != "" {
		cfg.S3Bucket = s
	}
	if s := os.Getenv("S3_ACCESS_KEY"); s != "" {
		cfg.S3AccessKey = s
	}
	if s := os.Getenv("S3_SECRET_KEY"); s != "" {
		cfg.S3SecretKey = s
	}
	if s := os.Getenv("S3_USE_PATH_STYLE"); s != "" {
		if v, err := strconv.ParseBool(s); err == nil {
			cfg.S3UsePathStyle = v
		}
	}
	if s := os.Getenv("S3_USE_SSL"); s != "" {
		if v, err := strconv.ParseBool(s); err == nil {
			cfg.S3UseSSL = v
		}
	}
	if s := os.Getenv("S3_PREFIX"); s != "" {
		cfg.S3Prefix = s
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
	cfg.DocumentServerURL = strings.TrimRight(cfg.DocumentServerURL, "/")
	cfg.DocumentServerPublicURL = strings.TrimRight(cfg.DocumentServerPublicURL, "/")
	cfg.StorageBackend = strings.ToLower(strings.TrimSpace(cfg.StorageBackend))
	if cfg.StorageBackend == "" {
		cfg.StorageBackend = "local"
	}
	if cfg.S3Region == "" {
		cfg.S3Region = "us-east-1"
	}
	cfg.S3Endpoint = strings.TrimRight(strings.TrimSpace(cfg.S3Endpoint), "/")
	cfg.S3Prefix = strings.Trim(strings.TrimSpace(cfg.S3Prefix), "/")
	return cfg, nil
}

// FromLiteral applies env overrides and resolves paths for a pre-populated
// Config (useful for testing).
func FromLiteral(cfg *Config) (*Config, error) {
	return applyEnvOverrides(cfg)
}
