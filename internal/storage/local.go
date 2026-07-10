package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type LocalStore struct {
	root    string
	mu      sync.Mutex
	docLock map[string]*sync.RWMutex
}

func NewLocalStore(root string) (*LocalStore, error) {
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	return &LocalStore{root: root, docLock: make(map[string]*sync.RWMutex)}, nil
}

func (s *LocalStore) Put(ctx context.Context, documentID string, reader io.Reader, meta Meta) error {
	lock := s.lockFor(documentID)
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Join(s.root, documentID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create doc dir: %w", err)
	}
	f, err := os.Create(filepath.Join(dir, "original"))
	if err != nil {
		return fmt.Errorf("create original file: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, reader); err != nil {
		return fmt.Errorf("write original file: %w", err)
	}
	metaData, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), metaData, 0644); err != nil {
		return fmt.Errorf("write meta: %w", err)
	}
	return nil
}

func (s *LocalStore) Create(ctx context.Context, documentID string, meta Meta) error {
	lock := s.lockFor(documentID)
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Join(s.root, documentID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create doc dir: %w", err)
	}
	return s.writeMetaLocked(documentID, &meta)
}

func (s *LocalStore) Stat(ctx context.Context, documentID string, variant Variant) (*Meta, *ObjectInfo, error) {
	lock := s.lockFor(documentID)
	lock.RLock()
	defer lock.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	meta, err := s.readMetaLocked(documentID)
	if err != nil {
		return nil, nil, err
	}
	path, err := s.pathForVariantLocked(documentID, variant)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	return meta, &ObjectInfo{
		Size:         info.Size(),
		ETag:         localETag(info),
		LastModified: info.ModTime(),
		ContentType:  "application/octet-stream",
	}, nil
}

func (s *LocalStore) Open(ctx context.Context, documentID string, variant Variant, byteRange *ByteRange) (io.ReadCloser, *Meta, *ObjectInfo, error) {
	lock := s.lockFor(documentID)
	lock.RLock()
	defer lock.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	meta, err := s.readMetaLocked(documentID)
	if err != nil {
		return nil, nil, nil, err
	}
	path, err := s.pathForVariantLocked(documentID, variant)
	if err != nil {
		return nil, nil, nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, nil, err
	}
	if byteRange != nil && (byteRange.Start < 0 || byteRange.End < byteRange.Start || byteRange.Start >= info.Size()) {
		return nil, nil, nil, ErrInvalidRange
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, nil, err
	}
	objectInfo := &ObjectInfo{
		Size:         info.Size(),
		ETag:         localETag(info),
		LastModified: info.ModTime(),
		ContentType:  "application/octet-stream",
	}
	if byteRange == nil {
		return f, meta, objectInfo, nil
	}
	end := byteRange.End
	if end >= info.Size() {
		end = info.Size() - 1
	}
	return &sectionReadCloser{
		SectionReader: io.NewSectionReader(f, byteRange.Start, end-byteRange.Start+1),
		close:         f.Close,
	}, meta, objectInfo, nil
}

func (s *LocalStore) PutEdited(ctx context.Context, documentID string, reader io.Reader) error {
	lock := s.lockFor(documentID)
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Join(s.root, documentID)
	f, err := os.Create(filepath.Join(dir, "edited"))
	if err != nil {
		return fmt.Errorf("create edited file: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, reader); err != nil {
		return fmt.Errorf("write edited file: %w", err)
	}
	return nil
}

func (s *LocalStore) GetMeta(ctx context.Context, documentID string) (*Meta, error) {
	lock := s.lockFor(documentID)
	lock.RLock()
	defer lock.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.readMetaLocked(documentID)
}

func (s *LocalStore) MarkEdited(ctx context.Context, documentID string) error {
	lock := s.lockFor(documentID)
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	meta, err := s.readMetaLocked(documentID)
	if err != nil {
		return err
	}
	meta.IsEdited = true
	meta.EditedAt = time.Now()
	return s.writeMetaLocked(documentID, meta)
}

func (s *LocalStore) ExtendTTL(ctx context.Context, documentID string, hours int) error {
	lock := s.lockFor(documentID)
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	meta, err := s.readMetaLocked(documentID)
	if err != nil {
		return err
	}
	meta.ExpiresAt = time.Now().Add(time.Duration(hours) * time.Hour)
	return s.writeMetaLocked(documentID, meta)
}

func (s *LocalStore) Delete(ctx context.Context, documentID string) error {
	lock := s.lockFor(documentID)
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(s.root, documentID))
}

func (s *LocalStore) Expire(ctx context.Context) (int, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	var cleaned int
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return cleaned, err
		}
		if !e.IsDir() {
			continue
		}
		lock := s.lockFor(e.Name())
		lock.Lock()
		meta, err := s.readMetaLocked(e.Name())
		if err != nil {
			lock.Unlock()
			continue
		}
		if meta.ExpiresAt.Before(now) {
			if err := os.RemoveAll(filepath.Join(s.root, e.Name())); err == nil {
				cleaned++
			}
		}
		lock.Unlock()
	}
	return cleaned, nil
}

func (s *LocalStore) readMetaLocked(documentID string) (*Meta, error) {
	dir := filepath.Join(s.root, documentID)
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil, err
	}
	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse meta: %w", err)
	}
	return &meta, nil
}

func (s *LocalStore) writeMetaLocked(documentID string, meta *Meta) error {
	data, _ := json.MarshalIndent(meta, "", "  ")
	return os.WriteFile(filepath.Join(s.root, documentID, "meta.json"), data, 0644)
}

func (s *LocalStore) pathForVariantLocked(documentID string, variant Variant) (string, error) {
	dir := filepath.Join(s.root, documentID)
	switch variant {
	case VariantOriginal:
		for _, name := range []string{"original", "original.docx"} {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
		return filepath.Join(dir, "original"), nil
	case VariantLatest:
		for _, name := range []string{"edited", "edited.docx", "original", "original.docx"} {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
		return "", os.ErrNotExist
	default:
		return "", fmt.Errorf("unknown storage variant: %s", variant)
	}
}

type sectionReadCloser struct {
	*io.SectionReader
	close func() error
}

func (r *sectionReadCloser) Close() error {
	return r.close()
}

func localETag(info os.FileInfo) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", info.Name(), info.Size(), info.ModTime().UnixNano())))
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

func (s *LocalStore) lockFor(documentID string) *sync.RWMutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, ok := s.docLock[documentID]
	if !ok {
		lock = &sync.RWMutex{}
		s.docLock[documentID] = lock
	}
	return lock
}
