package storage

import (
	"context"
	"fmt"

	"github.com/zenmind/onlyoffice-gateway/internal/config"
)

func NewStore(ctx context.Context, cfg *config.Config) (Store, error) {
	switch cfg.StorageBackend {
	case "", "local":
		return NewLocalStore(cfg.StorageDir)
	case "s3":
		return NewS3Store(ctx, S3Options{
			Endpoint:     cfg.S3Endpoint,
			Region:       cfg.S3Region,
			Bucket:       cfg.S3Bucket,
			AccessKey:    cfg.S3AccessKey,
			SecretKey:    cfg.S3SecretKey,
			UsePathStyle: cfg.S3UsePathStyle,
			UseSSL:       cfg.S3UseSSL,
			Prefix:       cfg.S3Prefix,
		})
	default:
		return nil, fmt.Errorf("unsupported storage backend: %s", cfg.StorageBackend)
	}
}
