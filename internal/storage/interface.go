package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrInvalidRange = errors.New("invalid byte range")

type Meta struct {
	DocumentID      string    `json:"document_id"`
	ServiceID       string    `json:"service_id"`
	ExternalID      string    `json:"external_id"`
	WebhookURL      string    `json:"webhook_url"`
	FileName        string    `json:"file_name"`
	FileType        string    `json:"file_type"`
	DocumentType    string    `json:"document_type"`
	EditorKey       string    `json:"editor_key"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	IsEdited        bool      `json:"is_edited"`
	EditedAt        time.Time `json:"edited_at,omitempty"`
	Branding        any       `json:"branding,omitempty"`
	ConfigOverrides any       `json:"config_overrides,omitempty"`
}

type Variant string

const (
	VariantOriginal Variant = "original"
	VariantLatest   Variant = "latest"
)

type ByteRange struct {
	Start int64
	End   int64
}

type ObjectInfo struct {
	Size         int64
	ETag         string
	LastModified time.Time
	ContentType  string
}

type Store interface {
	Put(ctx context.Context, documentID string, reader io.Reader, meta Meta) error
	Open(ctx context.Context, documentID string, variant Variant, byteRange *ByteRange) (io.ReadCloser, *Meta, *ObjectInfo, error)
	PutEdited(ctx context.Context, documentID string, reader io.Reader) error
	GetMeta(ctx context.Context, documentID string) (*Meta, error)
	MarkEdited(ctx context.Context, documentID string) error
	ExtendTTL(ctx context.Context, documentID string, hours int) error
	Delete(ctx context.Context, documentID string) error
	Expire(ctx context.Context) (int, error)
}
