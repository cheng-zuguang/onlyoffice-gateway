package storage

import (
	"encoding/base64"
	"encoding/json"
	"sort"
)

type attachmentCursor struct {
	CreatedAt  string `json:"created_at"`
	DocumentID string `json:"document_id"`
}

func pageAttachments(items []Meta, query AttachmentQuery) ([]Meta, string, error) {
	filtered := make([]Meta, 0, len(items))
	for _, item := range items {
		if query.ServiceID != "" && item.ServiceID != query.ServiceID {
			continue
		}
		filtered = append(filtered, item)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].DocumentID > filtered[j].DocumentID
		}
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})
	start := 0
	if query.Cursor != "" {
		cursor, err := decodeAttachmentCursor(query.Cursor)
		if err != nil {
			return nil, "", err
		}
		for start < len(filtered) && (filtered[start].CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00") != cursor.CreatedAt || filtered[start].DocumentID != cursor.DocumentID) {
			start++
		}
		if start < len(filtered) {
			start++
		}
	}
	limit := query.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	end := start + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	page := filtered[start:end]
	if end == len(filtered) || len(page) == 0 {
		return page, "", nil
	}
	return page, encodeAttachmentCursor(page[len(page)-1]), nil
}

func encodeAttachmentCursor(meta Meta) string {
	data, _ := json.Marshal(attachmentCursor{CreatedAt: meta.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"), DocumentID: meta.DocumentID})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeAttachmentCursor(raw string) (attachmentCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return attachmentCursor{}, err
	}
	var cursor attachmentCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return attachmentCursor{}, err
	}
	return cursor, nil
}
