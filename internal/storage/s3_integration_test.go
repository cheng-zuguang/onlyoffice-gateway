package storage_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zenmind/onlyoffice-gateway/internal/storage"
)

func TestS3StoreMinIOIntegration(t *testing.T) {
	if os.Getenv("RUN_MINIO_TESTS") != "1" {
		t.Skip("set RUN_MINIO_TESTS=1 and S3_* env vars to run MinIO/S3 integration tests")
	}
	bucket := os.Getenv("S3_BUCKET")
	if bucket == "" {
		t.Fatal("S3_BUCKET is required")
	}
	ctx := context.Background()
	store, err := storage.NewS3Store(ctx, storage.S3Options{
		Endpoint:     os.Getenv("S3_ENDPOINT"),
		Region:       envOrDefault("S3_REGION", "us-east-1"),
		Bucket:       bucket,
		AccessKey:    os.Getenv("S3_ACCESS_KEY"),
		SecretKey:    os.Getenv("S3_SECRET_KEY"),
		UsePathStyle: true,
		UseSSL:       os.Getenv("S3_USE_SSL") == "true",
		Prefix:       "integration-" + fmt.Sprint(time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("create s3 store: %v", err)
	}

	docID := "doc-minio"
	t.Cleanup(func() {
		_ = store.Delete(context.Background(), docID)
	})
	meta := storage.Meta{
		DocumentID:   docID,
		FileName:     "minio.docx",
		FileType:     "docx",
		DocumentType: "word",
		EditorKey:    "minio-key",
		CreatedAt:    time.Now().Add(-time.Minute),
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	if err := store.Put(ctx, docID, strings.NewReader("minio-original"), meta); err != nil {
		t.Fatalf("put original: %v", err)
	}

	rangeReader, _, info, err := store.Open(ctx, docID, storage.VariantOriginal, &storage.ByteRange{Start: 0, End: 4})
	if err != nil {
		t.Fatalf("open range: %v", err)
	}
	rangeBody, _ := io.ReadAll(rangeReader)
	rangeReader.Close()
	if string(rangeBody) != "minio" {
		t.Fatalf("expected ranged body minio, got %q", string(rangeBody))
	}
	if info.Size != int64(len("minio-original")) {
		t.Fatalf("expected full object size, got %d", info.Size)
	}

	if err := store.PutEdited(ctx, docID, strings.NewReader("minio-edited")); err != nil {
		t.Fatalf("put edited: %v", err)
	}
	if err := store.MarkEdited(ctx, docID); err != nil {
		t.Fatalf("mark edited: %v", err)
	}
	latestReader, latestMeta, _, err := store.Open(ctx, docID, storage.VariantLatest, nil)
	if err != nil {
		t.Fatalf("open latest: %v", err)
	}
	latestBody, _ := io.ReadAll(latestReader)
	latestReader.Close()
	if string(latestBody) != "minio-edited" || !latestMeta.IsEdited {
		t.Fatalf("expected latest edited document, body=%q edited=%v", string(latestBody), latestMeta.IsEdited)
	}

	latestMeta.ExpiresAt = time.Now().Add(-time.Minute)
	if err := store.Put(ctx, docID+"-expired", strings.NewReader("expired"), *latestMeta); err != nil {
		t.Fatalf("put expired: %v", err)
	}
	cleaned, err := store.Expire(ctx)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if cleaned < 1 {
		t.Fatalf("expected at least one expired document cleaned, got %d", cleaned)
	}
}

func envOrDefault(name, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
}
