package storage

import (
	"io"
	"time"
)

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

type Store interface {
	Put(documentID string, reader io.Reader, meta Meta) error
	Get(documentID string) (io.ReadCloser, error)
	GetOriginal(documentID string) (io.ReadSeekCloser, *Meta, error)
	PutEdited(documentID string, reader io.Reader) error
	GetMeta(documentID string) (*Meta, error)
	MarkEdited(documentID string) error
	ExtendTTL(documentID string, hours int) error
	Delete(documentID string) error
	Expire() (int, error)
}
