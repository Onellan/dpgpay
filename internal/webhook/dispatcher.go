package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"dpg-pay/internal/models"
)

type Dispatcher struct {
	store       *models.Store
	endpointURL string
	secret      string
	client      *http.Client
	maxAttempts int
}

func NewDispatcher(store *models.Store, endpointURL, secret string) *Dispatcher {
	return &Dispatcher{
		store:       store,
		endpointURL: strings.TrimSpace(endpointURL),
		secret:      secret,
		client: &http.Client{
			Timeout: 8 * time.Second,
		},
		maxAttempts: 8,
	}
}

func (d *Dispatcher) Enabled() bool {
	return d.endpointURL != ""
}

func (d *Dispatcher) Start(ctx context.Context) {
	if !d.Enabled() {
		log.Printf("webhook dispatcher disabled: WEBHOOK_ENDPOINT_URL is empty")
		return
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	log.Printf("webhook dispatcher started")

	for {
		select {
		case <-ctx.Done():
			log.Printf("webhook dispatcher stopping")
			return
		case <-ticker.C:
			d.processBatch(ctx)
		}
	}
}

func (d *Dispatcher) processBatch(ctx context.Context) {
	items, err := d.store.ListDueWebhookOutbox(ctx, 20)
	if err != nil {
		log.Printf("webhook dispatcher list due failed: %v", err)
		return
	}
	for _, item := range items {
		if err := d.deliverOne(ctx, item); err != nil {
			log.Printf("webhook delivery id=%d failed: %v", item.ID, err)
		}
	}
}

func (d *Dispatcher) deliverOne(ctx context.Context, item models.WebhookOutboxItem) error {
	if item.Attempts >= d.maxAttempts {
		return d.store.MarkWebhookRetry(ctx, item.ID, time.Now().UTC().Add(24*time.Hour), "max attempts exceeded")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpointURL, bytes.NewBufferString(item.Payload))
	if err != nil {
		next := nextAttemptDelay(item.Attempts)
		_ = d.store.MarkWebhookRetry(ctx, item.ID, time.Now().UTC().Add(next), truncateForDB(err.Error()))
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DPGPay-Event", item.EventType)
	req.Header.Set("X-DPGPay-Reference", item.ReferenceID)
	req.Header.Set("X-DPGPay-Signature", d.sign(item.Payload))

	resp, err := d.client.Do(req)
	if err != nil {
		next := nextAttemptDelay(item.Attempts)
		_ = d.store.MarkWebhookRetry(ctx, item.ID, time.Now().UTC().Add(next), truncateForDB(err.Error()))
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err := d.store.MarkWebhookSent(ctx, item.ID); err != nil {
			return err
		}
		_ = d.store.CreateAuditLog(ctx, "WEBHOOK_SENT", "webhook-dispatcher", item.ReferenceID, fmt.Sprintf(`{"outbox_id":%d,"status":%d}`, item.ID, resp.StatusCode))
		return nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	lastErr := fmt.Sprintf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	next := nextAttemptDelay(item.Attempts)
	if err := d.store.MarkWebhookRetry(ctx, item.ID, time.Now().UTC().Add(next), truncateForDB(lastErr)); err != nil {
		return err
	}
	return fmt.Errorf("webhook status=%d", resp.StatusCode)
}

func (d *Dispatcher) sign(payload string) string {
	if d.secret == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(d.secret))
	_, _ = mac.Write([]byte(payload))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func nextAttemptDelay(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	seconds := 5 << attempts
	if seconds > 1800 {
		seconds = 1800
	}
	return time.Duration(seconds) * time.Second
}

func truncateForDB(msg string) string {
	if len(msg) <= 400 {
		return msg
	}
	return msg[:400]
}
