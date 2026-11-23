package test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexandrmotologa/shadowtap/pkg/alerting"
	"github.com/alexandrmotologa/shadowtap/pkg/diff"
)

func TestAlerting_StatusDriftTrigger(t *testing.T) {
	var receivedAlerts atomic.Int64
	var lastEvent string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAlerts.Add(1)
		body, _ := io.ReadAll(r.Body)
		var p alerting.AlertPayload
		_ = json.Unmarshal(body, &p)
		lastEvent = p.Event
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mgr := alerting.NewWebhookAlertManager(server.URL, 0.05, true)

	// Status drift pair: Primary 200 vs Shadow 500
	pair := diff.ShadowPair{
		ID:        "tx-alert-1",
		Timestamp: time.Now(),
		Method:    "POST",
		Path:      "/api/v1/checkout",
		Primary: diff.ResponseSnapshot{
			StatusCode: 200,
		},
		Shadow: diff.ResponseSnapshot{
			StatusCode: 500,
		},
		Diff: diff.DiffResult{
			IsMatch:     false,
			StatusDrift: true,
		},
	}

	mgr.CheckAndAlert(pair, 0.01)

	// Wait briefly for async HTTP dispatch
	time.Sleep(50 * time.Millisecond)

	if receivedAlerts.Load() != 1 {
		t.Fatalf("expected 1 webhook alert sent, got %d", receivedAlerts.Load())
	}
	if lastEvent != "SEMANTIC_REGRESSION_ALERT" {
		t.Fatalf("unexpected event payload: %s", lastEvent)
	}
}
