package test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alexandrmotologa/shadowtap/pkg/diff"
	"github.com/alexandrmotologa/shadowtap/pkg/metrics"
	"github.com/alexandrmotologa/shadowtap/pkg/proxy"
	"github.com/alexandrmotologa/shadowtap/pkg/security"
	"github.com/alexandrmotologa/shadowtap/pkg/storage"
)

func BenchmarkProxyOverhead(b *testing.B) {
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"OK","id":100}`))
	}))
	defer primaryServer.Close()

	shadowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"OK","id":100}`))
	}))
	defer shadowServer.Close()

	telemetry := metrics.NewGlobalTelemetry()
	store := storage.NewRingBuffer(5000)

	maskCfg := diff.DefaultMaskConfig()
	dispCfg := proxy.DispatcherConfig{
		ShadowTarget:   shadowServer.URL,
		SampleRate:     1.0,
		QueueCapacity:  10000,
		WorkerCount:    16,
		RequestTimeout: 2 * time.Second,
		PIIConfig:      security.DefaultPIIConfig(),
		MutationConfig: security.DefaultMutationConfig(),
		MaskConfig:     &maskCfg,
	}

	dispatcher, err := proxy.NewShadowDispatcher(dispCfg, telemetry, store)
	if err != nil {
		b.Fatalf("failed to create dispatcher: %v", err)
	}
	defer dispatcher.Stop()

	proxySrv, err := proxy.NewServer(proxy.ProxyServerConfig{
		PrimaryTarget: primaryServer.URL,
		MaxBodyBytes:  1024 * 1024,
	}, dispatcher)
	if err != nil {
		b.Fatalf("failed to create proxy: %v", err)
	}

	req := httptest.NewRequest("GET", "/bench", nil)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		proxySrv.ServeHTTP(w, req)
		resp := w.Result()
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

func BenchmarkSemanticDiffing(b *testing.B) {
	snap1 := diff.NewResponseSnapshot(200, http.Header{}, `{"order_id":"ORD-991","amount":99.50,"items":["book","pen"],"timestamp":"2026-11-23T10:00:00Z"}`, 5*time.Millisecond, nil)
	snap2 := diff.NewResponseSnapshot(200, http.Header{}, `{"order_id":"ORD-991","amount":99.50,"items":["book","pen"],"timestamp":"2026-11-23T10:00:01Z"}`, 5*time.Millisecond, nil)

	maskCfg := diff.DefaultMaskConfig()
	opts := diff.DefaultCompareOptions()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = diff.CompareSnapshots(snap1, snap2, &maskCfg, opts)
	}
}
