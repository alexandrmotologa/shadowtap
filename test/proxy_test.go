package test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexandrmotologa/shadowtap/pkg/diff"
	"github.com/alexandrmotologa/shadowtap/pkg/metrics"
	"github.com/alexandrmotologa/shadowtap/pkg/proxy"
	"github.com/alexandrmotologa/shadowtap/pkg/security"
	"github.com/alexandrmotologa/shadowtap/pkg/storage"
)

func TestProxy_EndToEndShadowing(t *testing.T) {
	var primaryHits atomic.Int64
	var shadowHits atomic.Int64

	// 1. Mock Primary Backend
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"order_id": "ORD-123", "status": "APPROVED", "timestamp": "2026-11-23T15:00:00Z"}`))
	}))
	defer primaryServer.Close()

	// 2. Mock Shadow Backend
	shadowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		shadowHits.Add(1)
		// Check for safety shadow header
		if r.Header.Get("X-Shadow-Request") != "true" {
			t.Errorf("expected X-Shadow-Request header in shadow request")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Slight variation in timestamp (volatile noise)
		_, _ = w.Write([]byte(`{"order_id": "ORD-123", "status": "APPROVED", "timestamp": "2026-11-23T15:00:01Z"}`))
	}))
	defer shadowServer.Close()

	// 3. Initialize shadowtap subsystems
	telemetry := metrics.NewGlobalTelemetry()
	store := storage.NewRingBuffer(100)

	dispCfg := proxy.DispatcherConfig{
		ShadowTarget:   shadowServer.URL,
		SampleRate:     1.0,
		QueueCapacity:  100,
		WorkerCount:    2,
		RequestTimeout: 2 * time.Second,
		PIIConfig:      security.DefaultPIIConfig(),
		MutationConfig: security.DefaultMutationConfig(),
		MaskConfig:     diff.DefaultMaskConfig(),
	}

	dispatcher, err := proxy.NewShadowDispatcher(dispCfg, telemetry, store)
	if err != nil {
		t.Fatalf("failed to create dispatcher: %v", err)
	}
	defer dispatcher.Stop()

	proxyCfg := proxy.ProxyServerConfig{
		PrimaryTarget: primaryServer.URL,
		MaxBodyBytes:  1024 * 1024,
	}

	proxySrv, err := proxy.NewServer(proxyCfg, dispatcher)
	if err != nil {
		t.Fatalf("failed to create proxy server: %v", err)
	}

	// 4. Send request through shadowtap proxy
	req := httptest.NewRequest("POST", "/api/v1/orders", strings.NewReader(`{"item": "book", "qty": 1}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	proxySrv.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200 from primary, got %d", resp.StatusCode)
	}

	respBody, _ := io.ReadAll(resp.Body)
	var primParsed map[string]any
	if err := json.Unmarshal(respBody, &primParsed); err != nil {
		t.Fatalf("invalid json received from proxy: %v", err)
	}
	if primParsed["status"] != "APPROVED" {
		t.Fatalf("unexpected body from primary: %s", string(respBody))
	}

	// Wait briefly for asynchronous background shadow worker to complete
	time.Sleep(50 * time.Millisecond)

	if primaryHits.Load() != 1 {
		t.Fatalf("expected 1 primary hit, got %d", primaryHits.Load())
	}
	if shadowHits.Load() != 1 {
		t.Fatalf("expected 1 shadow hit, got %d", shadowHits.Load())
	}

	// Verify telemetry and storage recorded the transaction
	stats := telemetry.Stats()
	if stats.TotalShadowed != 1 {
		t.Fatalf("expected 1 shadowed request in telemetry, got %d", stats.TotalShadowed)
	}
	if stats.TotalRegressed != 0 {
		t.Fatalf("expected 0 regressions (timestamp noise ignored), got %d", stats.TotalRegressed)
	}

	pairs := store.Query(storage.QueryFilter{Limit: 10})
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair in ring buffer, got %d", len(pairs))
	}
	if !pairs[0].Diff.IsMatch {
		t.Fatalf("expected diff match to be true, got false")
	}
}
