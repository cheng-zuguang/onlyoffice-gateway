package storage

import (
	"bytes"
	"context"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

func TestStoreContract(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		store, err := NewLocalStore(t.TempDir())
		if err != nil {
			t.Fatalf("create local store: %v", err)
		}
		runStoreContract(t, store)
	})

	t.Run("s3", func(t *testing.T) {
		store := NewS3StoreWithClient(newFakeS3Client(), "bucket", "documents")
		runStoreContract(t, store)
	})
}

func runStoreContract(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().Add(-time.Minute).Truncate(time.Second)
	meta := Meta{
		DocumentID:   "doc-contract",
		FileName:     "contract.docx",
		FileType:     "docx",
		DocumentType: "word",
		EditorKey:    "editor-key",
		CreatedAt:    now,
		ExpiresAt:    now.Add(time.Hour),
	}

	if err := store.Put(ctx, meta.DocumentID, strings.NewReader("original-content"), meta); err != nil {
		t.Fatalf("put original: %v", err)
	}

	body, readMeta, info := openAndRead(t, store, meta.DocumentID, VariantOriginal, nil)
	if body != "original-content" {
		t.Fatalf("expected original content, got %q", body)
	}
	if readMeta.FileName != "contract.docx" {
		t.Fatalf("expected meta filename, got %q", readMeta.FileName)
	}
	if info.Size != int64(len("original-content")) {
		t.Fatalf("expected original size, got %d", info.Size)
	}

	ranged, _, _ := openAndRead(t, store, meta.DocumentID, VariantOriginal, &ByteRange{Start: 0, End: 7})
	if ranged != "original" {
		t.Fatalf("expected ranged original bytes, got %q", ranged)
	}

	if err := store.PutEdited(ctx, meta.DocumentID, strings.NewReader("edited-content")); err != nil {
		t.Fatalf("put edited: %v", err)
	}
	if err := store.MarkEdited(ctx, meta.DocumentID); err != nil {
		t.Fatalf("mark edited: %v", err)
	}
	latest, latestMeta, _ := openAndRead(t, store, meta.DocumentID, VariantLatest, nil)
	if latest != "edited-content" {
		t.Fatalf("expected latest edited content, got %q", latest)
	}
	if !latestMeta.IsEdited {
		t.Fatal("expected meta to be marked edited")
	}

	if err := store.ExtendTTL(ctx, meta.DocumentID, 2); err != nil {
		t.Fatalf("extend ttl: %v", err)
	}
	extended, err := store.GetMeta(ctx, meta.DocumentID)
	if err != nil {
		t.Fatalf("get extended meta: %v", err)
	}
	if time.Until(extended.ExpiresAt) < time.Hour {
		t.Fatalf("expected ttl extension, got %s", extended.ExpiresAt)
	}
}

func TestStoreContractExpireDeletesExpiredDocuments(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		store, err := NewLocalStore(t.TempDir())
		if err != nil {
			t.Fatalf("create local store: %v", err)
		}
		runExpireContract(t, store)
	})

	t.Run("s3", func(t *testing.T) {
		store := NewS3StoreWithClient(newFakeS3Client(), "bucket", "documents")
		runExpireContract(t, store)
	})
}

func runExpireContract(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	expired := Meta{
		DocumentID: "doc-expired",
		FileName:   "expired.docx",
		CreatedAt:  time.Now().Add(-3 * time.Hour),
		ExpiresAt:  time.Now().Add(-time.Hour),
	}
	active := Meta{
		DocumentID: "doc-active",
		FileName:   "active.docx",
		CreatedAt:  time.Now().Add(-time.Hour),
		ExpiresAt:  time.Now().Add(time.Hour),
	}
	if err := store.Put(ctx, expired.DocumentID, strings.NewReader("expired"), expired); err != nil {
		t.Fatalf("put expired: %v", err)
	}
	if err := store.Put(ctx, active.DocumentID, strings.NewReader("active"), active); err != nil {
		t.Fatalf("put active: %v", err)
	}
	cleaned, err := store.Expire(ctx)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if cleaned != 1 {
		t.Fatalf("expected one expired document cleaned, got %d", cleaned)
	}
	if _, err := store.GetMeta(ctx, expired.DocumentID); err == nil {
		t.Fatal("expected expired document metadata to be removed")
	}
	activeBody, _, _ := openAndRead(t, store, active.DocumentID, VariantOriginal, nil)
	if activeBody != "active" {
		t.Fatalf("expected active document to remain, got %q", activeBody)
	}
}

func openAndRead(t *testing.T, store Store, documentID string, variant Variant, byteRange *ByteRange) (string, *Meta, *ObjectInfo) {
	t.Helper()
	reader, meta, info, err := store.Open(context.Background(), documentID, variant, byteRange)
	if err != nil {
		t.Fatalf("open %s: %v", variant, err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read %s: %v", variant, err)
	}
	return string(data), meta, info
}

type fakeS3Object struct {
	body         []byte
	contentType  string
	etag         string
	lastModified time.Time
}

type fakeS3Client struct {
	mu      sync.Mutex
	objects map[string]fakeS3Object
}

func newFakeS3Client() *fakeS3Client {
	return &fakeS3Client{objects: make(map[string]fakeS3Object)}
}

func (c *fakeS3Client) PutObject(ctx context.Context, input *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := io.ReadAll(input.Body)
	if err != nil {
		return nil, err
	}
	key := aws.ToString(input.Key)
	c.objects[key] = fakeS3Object{
		body:         data,
		contentType:  aws.ToString(input.ContentType),
		etag:         `"` + key + `"`,
		lastModified: time.Now().UTC().Truncate(time.Second),
	}
	return &s3.PutObjectOutput{ETag: aws.String(c.objects[key].etag)}, nil
}

func (c *fakeS3Client) GetObject(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	obj, ok := c.objects[aws.ToString(input.Key)]
	if !ok {
		return nil, fakeS3NotFound()
	}
	body := obj.body
	if input.Range != nil {
		start, end, ok := parseFakeS3Range(aws.ToString(input.Range), int64(len(body)))
		if !ok {
			return nil, fakeS3NotFound()
		}
		body = body[start : end+1]
	}
	return &s3.GetObjectOutput{
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: aws.Int64(int64(len(body))),
		ContentType:   aws.String(obj.contentType),
		ETag:          aws.String(obj.etag),
		LastModified:  aws.Time(obj.lastModified),
	}, nil
}

func (c *fakeS3Client) HeadObject(ctx context.Context, input *s3.HeadObjectInput, opts ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	obj, ok := c.objects[aws.ToString(input.Key)]
	if !ok {
		return nil, fakeS3NotFound()
	}
	return &s3.HeadObjectOutput{
		ContentLength: aws.Int64(int64(len(obj.body))),
		ContentType:   aws.String(obj.contentType),
		ETag:          aws.String(obj.etag),
		LastModified:  aws.Time(obj.lastModified),
	}, nil
}

func (c *fakeS3Client) ListObjectsV2(ctx context.Context, input *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := aws.ToString(input.Prefix)
	keys := make([]string, 0)
	for key := range c.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := &s3.ListObjectsV2Output{IsTruncated: aws.Bool(false)}
	for _, key := range keys {
		out.Contents = append(out.Contents, types.Object{Key: aws.String(key)})
	}
	return out, nil
}

func (c *fakeS3Client) DeleteObjects(ctx context.Context, input *s3.DeleteObjectsInput, opts ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, object := range input.Delete.Objects {
		delete(c.objects, aws.ToString(object.Key))
	}
	return &s3.DeleteObjectsOutput{}, nil
}

func fakeS3NotFound() error {
	return &smithy.GenericAPIError{Code: "NoSuchKey", Message: "not found"}
}

func parseFakeS3Range(header string, size int64) (int64, int64, bool) {
	if !strings.HasPrefix(header, "bytes=") {
		return 0, 0, false
	}
	startText, endText, ok := strings.Cut(strings.TrimPrefix(header, "bytes="), "-")
	if !ok {
		return 0, 0, false
	}
	start, err := parseInt64(startText)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	end := size - 1
	if endText != "" {
		parsedEnd, err := parseInt64(endText)
		if err != nil || parsedEnd < start {
			return 0, 0, false
		}
		end = parsedEnd
		if end >= size {
			end = size - 1
		}
	}
	return start, end, true
}

func parseInt64(value string) (int64, error) {
	return strconv.ParseInt(value, 10, 64)
}
