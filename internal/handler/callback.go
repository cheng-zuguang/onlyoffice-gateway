package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
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
	store                    storage.Store
	webhookMaxRetries        int
	callbackCapabilitySecret string
	credentialResolver       WebhookCredentialResolver
	debounce                 map[string]*time.Timer
	debounceMu               sync.Mutex
	saveJobs                 chan CallbackBody
	enqueueMu                sync.Mutex
	closed                   bool
	workers                  sync.WaitGroup
	metrics                  CallbackMetrics
}

type CallbackOptions struct {
	QueueSize int
	Workers   int
}

func NewCallbackHandler(store storage.Store, maxRetries int, callbackCapabilitySecret string, credentialResolver WebhookCredentialResolver) *CallbackHandler {
	return NewCallbackHandlerWithOptions(store, maxRetries, callbackCapabilitySecret, credentialResolver, CallbackOptions{})
}

func NewCallbackHandlerWithOptions(store storage.Store, maxRetries int, callbackCapabilitySecret string, credentialResolver WebhookCredentialResolver, opts CallbackOptions) *CallbackHandler {
	if opts.QueueSize <= 0 {
		opts.QueueSize = 64
	}
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	h := &CallbackHandler{
		store:                    store,
		webhookMaxRetries:        maxRetries,
		callbackCapabilitySecret: callbackCapabilitySecret,
		credentialResolver:       credentialResolver,
		debounce:                 make(map[string]*time.Timer),
		saveJobs:                 make(chan CallbackBody, opts.QueueSize),
	}
	for i := 0; i < opts.Workers; i++ {
		h.workers.Add(1)
		go h.saveWorker()
	}
	return h
}

// Close stops pending debounce timers, rejects new save jobs, and waits for
// already queued saves to complete. It is safe to call more than once.
func (h *CallbackHandler) Close() {
	h.debounceMu.Lock()
	for _, timer := range h.debounce {
		timer.Stop()
	}
	h.debounce = make(map[string]*time.Timer)
	h.debounceMu.Unlock()

	h.enqueueMu.Lock()
	if h.closed {
		h.enqueueMu.Unlock()
		return
	}
	h.closed = true
	close(h.saveJobs)
	h.enqueueMu.Unlock()
	h.workers.Wait()
}

type CallbackMetrics struct {
	SaveQueuedTotal       atomic.Int64
	SaveDroppedTotal      atomic.Int64
	SaveSucceededTotal    atomic.Int64
	SaveFailedTotal       atomic.Int64
	WebhookSucceededTotal atomic.Int64
	WebhookFailedTotal    atomic.Int64
}

func (h *CallbackHandler) MetricsText() string {
	return fmt.Sprintf(
		"# HELP onlyoffice_gateway_callback_save_queued_total Callback save jobs accepted by the bounded queue.\n"+
			"# TYPE onlyoffice_gateway_callback_save_queued_total counter\n"+
			"onlyoffice_gateway_callback_save_queued_total %d\n"+
			"# HELP onlyoffice_gateway_callback_save_dropped_total Callback save jobs rejected because the queue was full.\n"+
			"# TYPE onlyoffice_gateway_callback_save_dropped_total counter\n"+
			"onlyoffice_gateway_callback_save_dropped_total %d\n"+
			"# HELP onlyoffice_gateway_callback_save_succeeded_total Callback save jobs completed successfully.\n"+
			"# TYPE onlyoffice_gateway_callback_save_succeeded_total counter\n"+
			"onlyoffice_gateway_callback_save_succeeded_total %d\n"+
			"# HELP onlyoffice_gateway_callback_save_failed_total Callback save jobs that failed.\n"+
			"# TYPE onlyoffice_gateway_callback_save_failed_total counter\n"+
			"onlyoffice_gateway_callback_save_failed_total %d\n"+
			"# HELP onlyoffice_gateway_webhook_succeeded_total Webhook deliveries completed successfully.\n"+
			"# TYPE onlyoffice_gateway_webhook_succeeded_total counter\n"+
			"onlyoffice_gateway_webhook_succeeded_total %d\n"+
			"# HELP onlyoffice_gateway_webhook_failed_total Webhook deliveries exhausted all retries.\n"+
			"# TYPE onlyoffice_gateway_webhook_failed_total counter\n"+
			"onlyoffice_gateway_webhook_failed_total %d\n",
		h.metrics.SaveQueuedTotal.Load(),
		h.metrics.SaveDroppedTotal.Load(),
		h.metrics.SaveSucceededTotal.Load(),
		h.metrics.SaveFailedTotal.Load(),
		h.metrics.WebhookSucceededTotal.Load(),
		h.metrics.WebhookFailedTotal.Load(),
	)
}

func (h *CallbackHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body CallbackBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": 1, "message": "invalid json"})
		return
	}
	if !validCallbackCapability(body.Key, r.URL.Query().Get("token"), h.callbackCapabilitySecret) {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": 1, "message": "invalid callback token"})
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
		var timer *time.Timer
		timer = time.AfterFunc(200*time.Millisecond, func() {
			h.enqueueSaving(body)
			h.debounceMu.Lock()
			if h.debounce[body.Key] == timer {
				delete(h.debounce, body.Key)
			}
			h.debounceMu.Unlock()
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

func (h *CallbackHandler) enqueueSaving(body CallbackBody) {
	h.enqueueMu.Lock()
	defer h.enqueueMu.Unlock()
	if h.closed {
		return
	}
	select {
	case h.saveJobs <- body:
		h.metrics.SaveQueuedTotal.Add(1)
	default:
		h.metrics.SaveDroppedTotal.Add(1)
		log.Printf("[callback] save queue full: key=%s", body.Key)
	}
}

func (h *CallbackHandler) saveWorker() {
	defer h.workers.Done()
	for body := range h.saveJobs {
		if h.processSaving(body) {
			h.metrics.SaveSucceededTotal.Add(1)
		} else {
			h.metrics.SaveFailedTotal.Add(1)
		}
	}
}

func (h *CallbackHandler) processSaving(body CallbackBody) bool {
	meta, err := h.store.GetMeta(context.Background(), body.Key)
	if err != nil {
		log.Printf("[callback] load document metadata: %v", err)
		return false
	}
	if meta.SourceURL != "" {
		go h.deliverWebhook(body.Key, body.URL)
		return true
	}
	resp, err := callbackHTTPClient.Get(body.URL)
	if err != nil {
		log.Printf("[callback] download edited file: %v", err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		log.Printf("[callback] download edited file: unexpected status=%d", resp.StatusCode)
		return false
	}

	if err := h.store.PutEdited(context.Background(), body.Key, resp.Body); err != nil {
		log.Printf("[callback] store edited file: %v", err)
		return false
	}
	if err := h.store.MarkEdited(context.Background(), body.Key); err != nil {
		log.Printf("[callback] mark edited: %v", err)
		return false
	}
	log.Printf("[callback] document saved: key=%s", body.Key)

	go h.deliverWebhook(body.Key, "")
	return true
}

func (h *CallbackHandler) deliverWebhook(documentID, editedURL string) {
	meta, err := h.store.GetMeta(context.Background(), documentID)
	if err != nil || meta.WebhookURL == "" {
		return
	}
	if h.credentialResolver == nil {
		log.Printf("[webhook] missing credential resolver: doc=%s service=%s", documentID, meta.ServiceID)
		h.metrics.WebhookFailedTotal.Add(1)
		return
	}
	secret, ok := h.credentialResolver.ActiveWebhookSecret(meta.ServiceID)
	if !ok {
		log.Printf("[webhook] active credential missing: doc=%s service=%s", documentID, meta.ServiceID)
		h.metrics.WebhookFailedTotal.Add(1)
		return
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"event":       "document.saved",
		"document_id": documentID,
		"external_id": meta.ExternalID,
		"status":      "ready",
		"edited_url":  editedURL,
	})

	deliveryID := "delivery_" + randomString(24)

	for attempt := 0; attempt <= h.webhookMaxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			backoff += time.Duration(rand.Int63n(int64(250 * time.Millisecond)))
			time.Sleep(backoff)
		}
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		signingInput := "v1\n" + meta.ServiceID + "\n" + timestamp + "\n" + deliveryID + "\n" + string(payload)
		sig := computeHMAC(signingInput, secret)
		req, _ := http.NewRequest("POST", meta.WebhookURL, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Gateway-Service-Id", meta.ServiceID)
		req.Header.Set("X-Gateway-Timestamp", timestamp)
		req.Header.Set("X-Gateway-Delivery-Id", deliveryID)
		req.Header.Set("X-Gateway-Signature", "v1="+sig)
		req.Header.Set("X-Gateway-Event", "document.saved")
		resp, err := callbackHTTPClient.Do(req)
		status := 0
		if resp != nil {
			status = resp.StatusCode
			resp.Body.Close()
		}
		if err == nil && status >= http.StatusOK && status < http.StatusMultipleChoices {
			log.Printf("[webhook] delivered: doc=%s attempt=%d", documentID, attempt)
			h.metrics.WebhookSucceededTotal.Add(1)
			return
		}
		if !shouldRetryWebhook(status, err) {
			log.Printf("[webhook] permanent failure: doc=%s attempt=%d status=%d", documentID, attempt, status)
			h.metrics.WebhookFailedTotal.Add(1)
			return
		}
		log.Printf("[webhook] retryable failure: doc=%s attempt=%d status=%d err=%v", documentID, attempt, status, err)
	}
	log.Printf("[webhook] giving up: doc=%s", documentID)
	h.metrics.WebhookFailedTotal.Add(1)
}

func shouldRetryWebhook(status int, err error) bool {
	if err != nil {
		return true
	}
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

func computeHMAC(data, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}

func callbackCapability(documentID, secret string) string {
	return computeHMAC("callback:"+documentID, secret)
}

func validCallbackCapability(documentID, token, secret string) bool {
	if documentID == "" || token == "" || secret == "" {
		return false
	}
	expected := callbackCapability(documentID, secret)
	return hmac.Equal([]byte(token), []byte(expected))
}
