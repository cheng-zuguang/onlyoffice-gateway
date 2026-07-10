// Package audit stores Gateway-local administrator and runtime audit events.
package audit

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Event struct {
	Time       time.Time `json:"time"`
	Level      string    `json:"level"`
	Type       string    `json:"type"`
	RequestID  string    `json:"request_id,omitempty"`
	DocumentID string    `json:"document_id,omitempty"`
	ServiceID  string    `json:"service_id,omitempty"`
	URL        string    `json:"url,omitempty"`
	InstanceID string    `json:"instance_id"`
	Method     string    `json:"method,omitempty"`
	Path       string    `json:"path,omitempty"`
	Status     int       `json:"status,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
}

type Query struct {
	Level      string
	Type       string
	RequestID  string
	DocumentID string
	Limit      int
	Cursor     string
}

// Log is an instance-local JSONL audit log. Writes are synchronous so callers
// can use a successful write as their authorization to complete a destructive
// administrator command.
type Log struct {
	dir        string
	retention  int
	instanceID string
	mu         sync.Mutex
}

func New(dir string, retentionDays int, instanceID string) (*Log, error) {
	if retentionDays < 1 || retentionDays > 90 {
		return nil, fmt.Errorf("audit retention must be between 1 and 90 days")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create audit log directory: %w", err)
	}
	return &Log{dir: dir, retention: retentionDays, instanceID: instanceID}, nil
}

func (l *Log) Write(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	event.URL = redactURL(event.URL)
	event.InstanceID = l.instanceID
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(l.dir, event.Time.UTC().Format("2006-01-02")+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync audit log: %w", err)
	}
	return l.removeExpiredLocked(event.Time.UTC())
}

func (l *Log) List(ctx context.Context, query Query) ([]Event, string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return nil, "", err
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			files = append(files, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	limit := query.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	items := make([]Event, 0)
	for _, name := range files {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		f, err := os.Open(filepath.Join(l.dir, name))
		if err != nil {
			return nil, "", err
		}
		var fileItems []Event
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var event Event
			if json.Unmarshal(scanner.Bytes(), &event) == nil && matches(event, query) {
				fileItems = append(fileItems, event)
			}
		}
		f.Close()
		if err := scanner.Err(); err != nil {
			return nil, "", err
		}
		sort.Slice(fileItems, func(i, j int) bool { return fileItems[i].Time.After(fileItems[j].Time) })
		items = append(items, fileItems...)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Time.After(items[j].Time) })
	offset, err := decodeCursor(query.Cursor)
	if err != nil {
		return nil, "", err
	}
	if offset > len(items) {
		offset = len(items)
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	next := ""
	if end < len(items) {
		next = encodeCursor(end)
	}
	return items[offset:end], next, nil
}

func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}
func decodeCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, err
	}
	offset, err := strconv.Atoi(string(data))
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("invalid audit cursor")
	}
	return offset, nil
}

func matches(event Event, query Query) bool {
	if event.Type == "http.request" {
		return false
	}
	return (query.Level == "" || event.Level == query.Level) && (query.Type == "" || event.Type == query.Type) && (query.RequestID == "" || event.RequestID == query.RequestID) && (query.DocumentID == "" || event.DocumentID == query.DocumentID)
}

func (l *Log) removeExpiredLocked(now time.Time) error {
	cutoff := now.AddDate(0, 0, -l.retention).Format("2006-01-02")
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") && strings.TrimSuffix(entry.Name(), ".jsonl") < cutoff {
			if err := os.Remove(filepath.Join(l.dir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func redactURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.RawQuery, u.Fragment, u.User = "", "", nil
	return u.String()
}
