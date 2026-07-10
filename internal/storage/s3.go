package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type S3Options struct {
	Endpoint     string
	Region       string
	Bucket       string
	AccessKey    string
	SecretKey    string
	UsePathStyle bool
	UseSSL       bool
	Prefix       string
}

type s3API interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	DeleteObjects(context.Context, *s3.DeleteObjectsInput, ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
}

type S3Store struct {
	client s3API
	bucket string
	prefix string
}

func NewS3Store(ctx context.Context, opts S3Options) (*S3Store, error) {
	if opts.Bucket == "" {
		return nil, errors.New("s3 bucket is required")
	}
	if opts.Region == "" {
		opts.Region = "us-east-1"
	}
	loadOptions := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(opts.Region),
	}
	if opts.AccessKey != "" || opts.SecretKey != "" {
		loadOptions = append(loadOptions, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(opts.AccessKey, opts.SecretKey, "")))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = opts.UsePathStyle
		if opts.Endpoint != "" {
			endpoint := opts.Endpoint
			if parsed, err := url.Parse(endpoint); err == nil && parsed.Scheme == "" {
				scheme := "https"
				if !opts.UseSSL {
					scheme = "http"
				}
				endpoint = scheme + "://" + endpoint
			}
			o.BaseEndpoint = aws.String(endpoint)
		}
	})
	return NewS3StoreWithClient(client, opts.Bucket, opts.Prefix), nil
}

func NewS3StoreWithClient(client s3API, bucket, prefix string) *S3Store {
	return &S3Store{
		client: client,
		bucket: bucket,
		prefix: normalizePrefix(prefix),
	}
}

func (s *S3Store) Put(ctx context.Context, documentID string, reader io.Reader, meta Meta) error {
	if err := s.putObject(ctx, s.objectKey(documentID, "original"), reader, "application/octet-stream"); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	return s.putObject(ctx, s.objectKey(documentID, "meta.json"), bytes.NewReader(data), "application/json")
}

func (s *S3Store) Create(ctx context.Context, documentID string, meta Meta) error {
	return s.writeMeta(ctx, documentID, &meta)
}

func (s *S3Store) Stat(ctx context.Context, documentID string, variant Variant) (*Meta, *ObjectInfo, error) {
	meta, err := s.GetMeta(ctx, documentID)
	if err != nil {
		return nil, nil, err
	}
	_, head, err := s.objectForVariant(ctx, documentID, variant)
	if err != nil {
		return nil, nil, err
	}
	return meta, objectInfoFromHead(head), nil
}

func (s *S3Store) Open(ctx context.Context, documentID string, variant Variant, byteRange *ByteRange) (io.ReadCloser, *Meta, *ObjectInfo, error) {
	meta, err := s.GetMeta(ctx, documentID)
	if err != nil {
		return nil, nil, nil, err
	}
	key, head, err := s.objectForVariant(ctx, documentID, variant)
	if err != nil {
		return nil, nil, nil, err
	}
	size := aws.ToInt64(head.ContentLength)
	if byteRange != nil && (byteRange.Start < 0 || byteRange.End < byteRange.Start || byteRange.Start >= size) {
		return nil, nil, nil, ErrInvalidRange
	}
	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}
	if byteRange != nil {
		end := byteRange.End
		if end >= size {
			end = size - 1
		}
		input.Range = aws.String(fmt.Sprintf("bytes=%d-%d", byteRange.Start, end))
	}
	out, err := s.client.GetObject(ctx, input)
	if err != nil {
		if isS3NotFound(err) {
			return nil, nil, nil, os.ErrNotExist
		}
		return nil, nil, nil, fmt.Errorf("get s3 object: %w", err)
	}
	return out.Body, meta, objectInfoFromHead(head), nil
}

func (s *S3Store) PutEdited(ctx context.Context, documentID string, reader io.Reader) error {
	return s.putObject(ctx, s.objectKey(documentID, "edited"), reader, "application/octet-stream")
}

func (s *S3Store) GetMeta(ctx context.Context, documentID string) (*Meta, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(documentID, "meta.json")),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("get s3 meta: %w", err)
	}
	defer out.Body.Close()
	var meta Meta
	if err := json.NewDecoder(out.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("parse meta: %w", err)
	}
	return &meta, nil
}

func (s *S3Store) MarkEdited(ctx context.Context, documentID string) error {
	meta, err := s.GetMeta(ctx, documentID)
	if err != nil {
		return err
	}
	meta.IsEdited = true
	meta.EditedAt = time.Now()
	return s.writeMeta(ctx, documentID, meta)
}

func (s *S3Store) ExtendTTL(ctx context.Context, documentID string, hours int) error {
	meta, err := s.GetMeta(ctx, documentID)
	if err != nil {
		return err
	}
	meta.ExpiresAt = time.Now().Add(time.Duration(hours) * time.Hour)
	return s.writeMeta(ctx, documentID, meta)
}

func (s *S3Store) Delete(ctx context.Context, documentID string) error {
	prefix := s.documentPrefix(documentID)
	var token *string
	var objects []types.ObjectIdentifier
	for {
		out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return fmt.Errorf("list s3 document objects: %w", err)
		}
		for _, obj := range out.Contents {
			if obj.Key != nil {
				key := *obj.Key
				objects = append(objects, types.ObjectIdentifier{Key: &key})
			}
		}
		if !aws.ToBool(out.IsTruncated) || out.NextContinuationToken == nil {
			break
		}
		token = out.NextContinuationToken
	}
	if len(objects) == 0 {
		return nil
	}
	for len(objects) > 0 {
		batchSize := 1000
		if len(objects) < batchSize {
			batchSize = len(objects)
		}
		_, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(s.bucket),
			Delete: &types.Delete{Objects: objects[:batchSize]},
		})
		if err != nil {
			return fmt.Errorf("delete s3 document objects: %w", err)
		}
		objects = objects[batchSize:]
	}
	return nil
}

func (s *S3Store) Expire(ctx context.Context) (int, error) {
	var token *string
	now := time.Now()
	cleaned := 0
	seen := make(map[string]struct{})
	for {
		out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(s.prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return cleaned, fmt.Errorf("list s3 objects: %w", err)
		}
		for _, obj := range out.Contents {
			if obj.Key == nil || !strings.HasSuffix(*obj.Key, "/meta.json") {
				continue
			}
			docID, ok := s.documentIDFromMetaKey(*obj.Key)
			if !ok {
				continue
			}
			if _, ok := seen[docID]; ok {
				continue
			}
			seen[docID] = struct{}{}
			meta, err := s.GetMeta(ctx, docID)
			if err != nil {
				continue
			}
			if meta.ExpiresAt.Before(now) {
				if err := s.Delete(ctx, docID); err == nil {
					cleaned++
				}
			}
		}
		if !aws.ToBool(out.IsTruncated) || out.NextContinuationToken == nil {
			break
		}
		token = out.NextContinuationToken
	}
	return cleaned, nil
}

func (s *S3Store) putObject(ctx context.Context, key string, reader io.Reader, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        reader,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("put s3 object %s: %w", key, err)
	}
	return nil
}

func (s *S3Store) writeMeta(ctx context.Context, documentID string, meta *Meta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	return s.putObject(ctx, s.objectKey(documentID, "meta.json"), bytes.NewReader(data), "application/json")
}

func (s *S3Store) objectForVariant(ctx context.Context, documentID string, variant Variant) (string, *s3.HeadObjectOutput, error) {
	switch variant {
	case VariantOriginal:
		return s.headObject(ctx, s.objectKey(documentID, "original"))
	case VariantLatest:
		key, head, err := s.headObject(ctx, s.objectKey(documentID, "edited"))
		if err == nil {
			return key, head, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", nil, err
		}
		return s.headObject(ctx, s.objectKey(documentID, "original"))
	default:
		return "", nil, fmt.Errorf("unknown storage variant: %s", variant)
	}
}

func (s *S3Store) headObject(ctx context.Context, key string) (string, *s3.HeadObjectOutput, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isS3NotFound(err) {
			return "", nil, os.ErrNotExist
		}
		return "", nil, fmt.Errorf("head s3 object %s: %w", key, err)
	}
	return key, out, nil
}

func (s *S3Store) objectKey(documentID, name string) string {
	return s.documentPrefix(documentID) + name
}

func (s *S3Store) documentPrefix(documentID string) string {
	return s.prefix + documentID + "/"
}

func (s *S3Store) documentIDFromMetaKey(key string) (string, bool) {
	rest := strings.TrimPrefix(key, s.prefix)
	docID, name, ok := strings.Cut(rest, "/")
	return docID, ok && docID != "" && name == "meta.json"
}

func normalizePrefix(prefix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return ""
	}
	return prefix + "/"
}

func objectInfoFromHead(head *s3.HeadObjectOutput) *ObjectInfo {
	info := &ObjectInfo{
		Size:         aws.ToInt64(head.ContentLength),
		ETag:         aws.ToString(head.ETag),
		LastModified: aws.ToTime(head.LastModified),
		ContentType:  aws.ToString(head.ContentType),
	}
	if info.ContentType == "" {
		info.ContentType = "application/octet-stream"
	}
	return info
}

func isS3NotFound(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "NoSuchKey", "NotFound", "404":
		return true
	default:
		return false
	}
}
