package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/zenmind/onlyoffice-gateway/internal/storage"
)

var callbackHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

type CallbackBody struct {
	Key           string   `json:"key"`
	Status        int      `json:"status"`
	URL           string   `json:"url"`
	FileType      string   `json:"filetype"`
	Users         []string `json:"users"`
	ForceSaveType int      `json:"forcesavetype"`
}

type CallbackHandler struct {
	store             storage.Store
	webhookMaxRetries int
	webhookHMACSecret string
	debounce          map[string]*time.Timer
	debounceMu        sync.Mutex
}

func NewCallbackHandler(store storage.Store, maxRetries int, hmacSecret string) *CallbackHandler {
	return &CallbackHandler{
		store:             store,
		webhookMaxRetries: maxRetries,
		webhookHMACSecret: hmacSecret,
		debounce:          make(map[string]*time.Timer),
	}
}

func (h *CallbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body CallbackBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": 1, "message": "invalid json"})
		return
	}

	switch body.Status {
	case 2, 6:
		if body.URL == "" {
			writeJSON(w, http.StatusOK, map[string]interface{}{"error": 1})
			return
		}
		// Debounce: cancel any pending timer for this document
		h.debounceMu.Lock()
		if oldTimer, ok := h.debounce[body.Key]; ok {
			oldTimer.Stop()
		}
		timer := time.AfterFunc(200*time.Millisecond, func() {
			h.processSaving(body)
		})
		h.debounce[body.Key] = timer
		h.debounceMu.Unlock()

	case 1:
		if err := h.store.ExtendTTL(r.Context(), body.Key, 8); err != nil {
			log.Printf("[callback] extend ttl: %v", err)
		}

	case 4:
		// Closed with no changes — no-op

	default:
		log.Printf("[callback] unhandled status: %d", body.Status)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"error": 0})
}

func (h *CallbackHandler) processSaving(body CallbackBody) {
	resp, err := callbackHTTPClient.Get(body.URL)
	if err != nil {
		log.Printf("[callback] download edited file: %v", err)
		return
	}
	defer resp.Body.Close()

	if err := h.store.PutEdited(context.Background(), body.Key, resp.Body); err != nil {
		log.Printf("[callback] store edited file: %v", err)
		return
	}
	if err := h.store.MarkEdited(context.Background(), body.Key); err != nil {
		log.Printf("[callback] mark edited: %v", err)
		return
	}
	log.Printf("[callback] document saved: key=%s", body.Key)

	go h.deliverWebhook(body.Key)
}

func (h *CallbackHandler) deliverWebhook(documentID string) {
	meta, err := h.store.GetMeta(context.Background(), documentID)
	if err != nil || meta.WebhookURL == "" {
		return
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"event":       "document.saved",
		"document_id": documentID,
		"external_id": meta.ExternalID,
		"status":      "ready",
	})

	// HMAC signature: sha256(webhook_url + body, secret)
	sig := computeHMAC(meta.WebhookURL+string(payload), h.webhookHMACSecret)

	for attempt := 0; attempt <= h.webhookMaxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			time.Sleep(backoff)
		}
		req, _ := http.NewRequest("POST", meta.WebhookURL, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Gateway-Signature", "sha256="+sig)
		req.Header.Set("X-Gateway-Event", "document.saved")
		resp, err := callbackHTTPClient.Do(req)
		if err == nil && resp.StatusCode < 400 {
			resp.Body.Close()
			log.Printf("[webhook] delivered: doc=%s attempt=%d", documentID, attempt)
			return
		}
		if resp != nil {
			resp.Body.Close()
		}
		log.Printf("[webhook] failed: doc=%s attempt=%d err=%v", documentID, attempt, err)
	}
	log.Printf("[webhook] giving up: doc=%s", documentID)
}

func computeHMAC(data, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}
