package storage

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type LocalStore struct {
	root string
	mu   sync.RWMutex
}

func NewLocalStore(root string) (*LocalStore, error) {
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	return &LocalStore{root: root}, nil
}

func (s *LocalStore) Put(documentID string, reader io.Reader, meta Meta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.root, documentID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create doc dir: %w", err)
	}
	f, err := os.Create(filepath.Join(dir, "original.docx"))
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

func (s *LocalStore) Get(documentID string) (io.ReadCloser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dir := filepath.Join(s.root, documentID)
	for _, name := range []string{"edited.docx", "original.docx"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return os.Open(p)
		}
	}
	return nil, os.ErrNotExist
}

func (s *LocalStore) PutEdited(documentID string, reader io.Reader) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.root, documentID)
	f, err := os.Create(filepath.Join(dir, "edited.docx"))
	if err != nil {
		return fmt.Errorf("create edited file: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, reader); err != nil {
		return fmt.Errorf("write edited file: %w", err)
	}
	return nil
}

func (s *LocalStore) GetMeta(documentID string) (*Meta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readMetaLocked(documentID)
}

func (s *LocalStore) MarkEdited(documentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMetaLocked(documentID)
	if err != nil {
		return err
	}
	meta.IsEdited = true
	meta.EditedAt = time.Now()
	return s.writeMetaLocked(documentID, meta)
}

func (s *LocalStore) ExtendTTL(documentID string, hours int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMetaLocked(documentID)
	if err != nil {
		return err
	}
	meta.ExpiresAt = time.Now().Add(time.Duration(hours) * time.Hour)
	return s.writeMetaLocked(documentID, meta)
}

func (s *LocalStore) Delete(documentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.RemoveAll(filepath.Join(s.root, documentID))
}

func (s *LocalStore) Expire() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	var cleaned int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		meta, err := s.readMetaLocked(e.Name())
		if err != nil {
			continue
		}
		if meta.ExpiresAt.Before(now) {
			if err := os.RemoveAll(filepath.Join(s.root, e.Name())); err == nil {
				cleaned++
			}
		}
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
