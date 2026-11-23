package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/alexandrmotologa/shadowtap/pkg/diff"
)

// AlertPayload defines the structured notification sent to Slack/Discord/generic webhooks.
type AlertPayload struct {
	Event         string          `json:"event"`
	Timestamp     string          `json:"timestamp"`
	Route         string          `json:"route"`
	Method        string          `json:"method"`
	PrimaryStatus int             `json:"primary_status"`
	ShadowStatus  int             `json:"shadow_status"`
	GlobalSRR     float64         `json:"global_srr"`
	Message       string          `json:"message"`
	Mismatches    []diff.Mismatch `json:"mismatches,omitempty"`
}

// WebhookAlertManager handles automated notifications when regressions or status drifts occur.
type WebhookAlertManager struct {
	webhookURL         string
	srrThreshold       float64
	alertOnStatusDrift bool
	client             *http.Client
	mu                 sync.Mutex
	lastAlertTime      time.Time
	cooldown           time.Duration
}

// NewWebhookAlertManager creates an alert coordinator.
func NewWebhookAlertManager(webhookURL string, srrThreshold float64, alertOnStatusDrift bool) *WebhookAlertManager {
	return &WebhookAlertManager{
		webhookURL:         webhookURL,
		srrThreshold:       srrThreshold,
		alertOnStatusDrift: alertOnStatusDrift,
		client:             &http.Client{Timeout: 5 * time.Second},
		cooldown:           60 * time.Second, // Throttle alerts to avoid webhook spam
	}
}

// CheckAndAlert evaluates if an incoming transaction pair or global SRR justifies triggering an alert.
func (am *WebhookAlertManager) CheckAndAlert(pair diff.ShadowPair, currentSRR float64) {
	if am == nil || am.webhookURL == "" {
		return
	}

	shouldAlert := false
	alertReason := ""

	// 1. Critical status drift alert (e.g. Primary 200, Shadow 500)
	if am.alertOnStatusDrift && pair.Diff.StatusDrift {
		shouldAlert = true
		alertReason = fmt.Sprintf("Critical HTTP status drift on %s %s: Primary returned %d, Shadow returned %d",
			pair.Method, pair.Path, pair.Primary.StatusCode, pair.Shadow.StatusCode)
	}

	// 2. High SRR threshold alert
	if !shouldAlert && am.srrThreshold > 0 && currentSRR > am.srrThreshold {
		am.mu.Lock()
		now := time.Now()
		if now.Sub(am.lastAlertTime) > am.cooldown {
			shouldAlert = true
			alertReason = fmt.Sprintf("Semantic Regression Rate (SRR) exceeded threshold: %.2f%% > %.2f%%",
				currentSRR*100, am.srrThreshold*100)
			am.lastAlertTime = now
		}
		am.mu.Unlock()
	}

	if !shouldAlert {
		return
	}

	payload := AlertPayload{
		Event:         "SEMANTIC_REGRESSION_ALERT",
		Timestamp:     time.Now().Format(time.RFC3339),
		Route:         pair.Path,
		Method:        pair.Method,
		PrimaryStatus: pair.Primary.StatusCode,
		ShadowStatus:  pair.Shadow.StatusCode,
		GlobalSRR:     currentSRR,
		Message:       alertReason,
		Mismatches:    pair.Diff.Mismatches,
	}

	// Dispatch asynchronously
	go am.sendWebhook(payload)
}

func (am *WebhookAlertManager) sendWebhook(payload AlertPayload) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", am.webhookURL, bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := am.client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}
