package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr                 string        `yaml:"listen_addr"`
	DocumentServerURL          string        `yaml:"document_server_url"`
	DocumentServerPublicURL    string        `yaml:"document_server_public_url"`
	DocumentServerJWTSecret    string        `yaml:"document_server_jwt_secret"`
	AdminSessionSecret         string        `yaml:"gateway_admin_session_secret"`
	CallbackCapabilitySecret   string        `yaml:"gateway_callback_capability_secret"`
	WebhookSecretEncryptionKey string        `yaml:"webhook_secret_encryption_key"`
	StorageBackend             string        `yaml:"storage_backend"`
	StorageDir                 string        `yaml:"storage_dir"`
	S3Endpoint                 string        `yaml:"s3_endpoint"`
	S3Region                   string        `yaml:"s3_region"`
	S3Bucket                   string        `yaml:"s3_bucket"`
	S3AccessKey                string        `yaml:"s3_access_key"`
	S3SecretKey                string        `yaml:"s3_secret_key"`
	S3UsePathStyle             bool          `yaml:"s3_use_path_style"`
	S3UseSSL                   bool          `yaml:"s3_use_ssl"`
	S3Prefix                   string        `yaml:"s3_prefix"`
	MaxUploadBytes             int64         `yaml:"max_upload_bytes"`
	TTLHours                   int           `yaml:"ttl_hours"`
	CleanupInterval            time.Duration `yaml:"cleanup_interval"`
	WebhookMaxRetries          int           `yaml:"webhook_max_retries"`
	CallbackQueueSize          int           `yaml:"callback_queue_size"`
	CallbackWorkers            int           `yaml:"callback_workers"`
	AdminAuditLogDir           string        `yaml:"admin_audit_log_dir"`
	AdminAuditRetentionDays    int           `yaml:"admin_audit_log_retention_days"`
}

// UnmarshalYAML accepts the human-readable duration syntax used by environment
// variables (for example "15m" or "1h") for cleanup_interval as well.
func (cfg *Config) UnmarshalYAML(value *yaml.Node) error {
	var cleanupInterval *time.Duration
	content := make([]*yaml.Node, 0, len(value.Content))
	for i := 0; i+1 < len(value.Content); i += 2 {
		key, field := value.Content[i], value.Content[i+1]
		if key.Value != "cleanup_interval" {
			content = append(content, key, field)
			continue
		}
		if field.Kind != yaml.ScalarNode {
			return fmt.Errorf("cleanup_interval must be a duration string")
		}
		duration, err := time.ParseDuration(field.Value)
		if err != nil {
			return fmt.Errorf("cleanup_interval: %w", err)
		}
		cleanupInterval = &duration
	}

	type plain Config
	decoded := plain(*cfg)
	withoutCleanup := *value
	withoutCleanup.Content = content
	if err := withoutCleanup.Decode(&decoded); err != nil {
		return err
	}
	*cfg = Config(decoded)
	if cleanupInterval != nil {
		cfg.CleanupInterval = *cleanupInterval
	}
	return nil
}

func Defaults() *Config {
	return &Config{
		ListenAddr:              ":18080",
		StorageBackend:          "local",
		StorageDir:              "./data/storage",
		S3Region:                "us-east-1",
		S3UsePathStyle:          true,
		S3UseSSL:                true,
		MaxUploadBytes:          100 << 20,
		TTLHours:                8,
		CleanupInterval:         time.Hour,
		WebhookMaxRetries:       3,
		CallbackQueueSize:       64,
		CallbackWorkers:         4,
		AdminAuditLogDir:        "./data/audit",
		AdminAuditRetentionDays: 14,
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
	if s := os.Getenv("DOCUMENT_SERVER_JWT_SECRET"); s != "" {
		cfg.DocumentServerJWTSecret = s
	}
	if s := os.Getenv("GATEWAY_ADMIN_SESSION_SECRET"); s != "" {
		cfg.AdminSessionSecret = s
	}
	if s := os.Getenv("GATEWAY_CALLBACK_CAPABILITY_SECRET"); s != "" {
		cfg.CallbackCapabilitySecret = s
	}
	if s := os.Getenv("WEBHOOK_SECRET_ENCRYPTION_KEY"); s != "" {
		cfg.WebhookSecretEncryptionKey = s
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
	if s := os.Getenv("MAX_UPLOAD_BYTES"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			cfg.MaxUploadBytes = v
		}
	}
	if s := os.Getenv("TTL_HOURS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			cfg.TTLHours = v
		}
	}
	if s := os.Getenv("CLEANUP_INTERVAL"); s != "" {
		if v, err := time.ParseDuration(s); err == nil {
			cfg.CleanupInterval = v
		}
	}
	if s := os.Getenv("WEBHOOK_MAX_RETRIES"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			cfg.WebhookMaxRetries = v
		}
	}
	if s := os.Getenv("CALLBACK_QUEUE_SIZE"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			cfg.CallbackQueueSize = v
		}
	}
	if s := os.Getenv("CALLBACK_WORKERS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			cfg.CallbackWorkers = v
		}
	}
	if s := os.Getenv("ADMIN_AUDIT_LOG_DIR"); s != "" {
		cfg.AdminAuditLogDir = s
	}
	if s := os.Getenv("ADMIN_AUDIT_LOG_RETENTION_DAYS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			cfg.AdminAuditRetentionDays = v
		}
	}

	var err error
	cfg.StorageDir, err = filepath.Abs(cfg.StorageDir)
	if err != nil {
		return nil, fmt.Errorf("resolve storage dir: %w", err)
	}
	cfg.AdminAuditLogDir, err = filepath.Abs(cfg.AdminAuditLogDir)
	if err != nil {
		return nil, fmt.Errorf("resolve audit log dir: %w", err)
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
	if cfg.MaxUploadBytes <= 0 {
		cfg.MaxUploadBytes = 100 << 20
	}
	if cfg.CleanupInterval <= 0 {
		cfg.CleanupInterval = time.Hour
	}
	if cfg.CallbackQueueSize <= 0 {
		cfg.CallbackQueueSize = 64
	}
	if cfg.CallbackWorkers <= 0 {
		cfg.CallbackWorkers = 4
	}
	if cfg.AdminAuditRetentionDays <= 0 {
		cfg.AdminAuditRetentionDays = 14
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

// Validate verifies the configuration values required to safely serve requests.
func (cfg *Config) Validate() error {
	secrets := []struct {
		name  string
		value string
	}{
		{"DOCUMENT_SERVER_JWT_SECRET", cfg.DocumentServerJWTSecret},
		{"GATEWAY_ADMIN_SESSION_SECRET", cfg.AdminSessionSecret},
		{"GATEWAY_CALLBACK_CAPABILITY_SECRET", cfg.CallbackCapabilitySecret},
	}
	for _, secret := range secrets {
		if len(strings.TrimSpace(secret.value)) < 32 {
			return fmt.Errorf("%s must be at least 32 characters", secret.name)
		}
	}
	if _, err := cfg.WebhookSecretEncryptionKeyBytes(); err != nil {
		return err
	}
	allSecretValues := []string{
		cfg.DocumentServerJWTSecret,
		cfg.AdminSessionSecret,
		cfg.CallbackCapabilitySecret,
		cfg.WebhookSecretEncryptionKey,
	}
	for i := range allSecretValues {
		for j := i + 1; j < len(allSecretValues); j++ {
			if allSecretValues[i] == allSecretValues[j] {
				return fmt.Errorf("gateway secrets must be distinct")
			}
		}
	}
	if strings.TrimSpace(cfg.DocumentServerURL) == "" {
		return fmt.Errorf("DOCUMENT_SERVER_URL is required")
	}
	if cfg.TTLHours <= 0 {
		return fmt.Errorf("TTL_HOURS must be greater than zero")
	}
	if cfg.MaxUploadBytes <= 0 {
		return fmt.Errorf("MAX_UPLOAD_BYTES must be greater than zero")
	}
	if cfg.CleanupInterval <= 0 {
		return fmt.Errorf("CLEANUP_INTERVAL must be greater than zero")
	}
	if cfg.CallbackQueueSize <= 0 {
		return fmt.Errorf("CALLBACK_QUEUE_SIZE must be greater than zero")
	}
	if cfg.CallbackWorkers <= 0 {
		return fmt.Errorf("CALLBACK_WORKERS must be greater than zero")
	}
	if cfg.AdminAuditRetentionDays < 1 || cfg.AdminAuditRetentionDays > 90 {
		return fmt.Errorf("ADMIN_AUDIT_LOG_RETENTION_DAYS must be between 1 and 90")
	}
	switch cfg.StorageBackend {
	case "local":
		if strings.TrimSpace(cfg.StorageDir) == "" {
			return fmt.Errorf("STORAGE_DIR is required for local storage")
		}
	case "s3":
		if strings.TrimSpace(cfg.S3Bucket) == "" {
			return fmt.Errorf("S3_BUCKET is required for s3 storage")
		}
	default:
		return fmt.Errorf("unsupported STORAGE_BACKEND %q", cfg.StorageBackend)
	}
	return nil
}

func (cfg *Config) WebhookSecretEncryptionKeyBytes() ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(cfg.WebhookSecretEncryptionKey))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("WEBHOOK_SECRET_ENCRYPTION_KEY must be base64 encoding of exactly 32 bytes")
	}
	return key, nil
}
