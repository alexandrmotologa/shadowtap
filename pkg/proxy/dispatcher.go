package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/alexandrmotologa/shadowtap/pkg/alerting"
	"github.com/alexandrmotologa/shadowtap/pkg/diff"
	"github.com/alexandrmotologa/shadowtap/pkg/metrics"
	"github.com/alexandrmotologa/shadowtap/pkg/recorder"
	"github.com/alexandrmotologa/shadowtap/pkg/security"
	"github.com/alexandrmotologa/shadowtap/pkg/storage"
)

// ShadowTask pairs a captured primary transaction with its cloned request for async replay.
type ShadowTask struct {
	ID              string
	Timestamp       time.Time
	Request         *ClonedRequest
	PrimarySnapshot diff.ResponseSnapshot
	SamplingRate    float64
}

// DispatcherConfig tunes concurrency, sampling, and timeout parameters.
type DispatcherConfig struct {
	ShadowTarget   string
	SampleRate     float64
	QueueCapacity  int
	WorkerCount    int
	RequestTimeout time.Duration
	PIIConfig      security.PIIConfig
	MutationConfig security.MutationConfig
	MaskConfig     *diff.MaskConfig
	CompareOptions diff.CompareOptions
	ShadowHeaders  map[string]string
	Recorder       *recorder.TrafficRecorder
	AlertManager   *alerting.WebhookAlertManager
}

// ShadowDispatcher coordinates the non-blocking shadow worker pool.
type ShadowDispatcher struct {
	config     DispatcherConfig
	shadowURL  *url.URL
	httpClient *http.Client
	queue      chan ShadowTask
	circuit    *CircuitBreaker
	telemetry  *metrics.GlobalTelemetry
	storage    *storage.RingBuffer
	stopCh     chan struct{}
	wg         sync.WaitGroup
	rndMu      sync.Mutex
	rnd        *rand.Rand
}

// NewShadowDispatcher constructs and starts the background worker pool.
func NewShadowDispatcher(cfg DispatcherConfig, tel *metrics.GlobalTelemetry, store *storage.RingBuffer) (*ShadowDispatcher, error) {
	parsedURL, err := url.Parse(cfg.ShadowTarget)
	if err != nil {
		return nil, fmt.Errorf("invalid shadow target url %q: %w", cfg.ShadowTarget, err)
	}

	if cfg.QueueCapacity <= 0 {
		cfg.QueueCapacity = 5000
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 16
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 8 * time.Second
	}
	if cfg.SampleRate <= 0 {
		cfg.SampleRate = 1.0
	}
	if cfg.MaskConfig == nil {
		defaultMask := diff.DefaultMaskConfig()
		cfg.MaskConfig = &defaultMask
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          1000,
		MaxIdleConnsPerHost:   200,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: false},
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   cfg.RequestTimeout,
	}

	d := &ShadowDispatcher{
		config:     cfg,
		shadowURL:  parsedURL,
		httpClient: client,
		queue:      make(chan ShadowTask, cfg.QueueCapacity),
		circuit:    NewCircuitBreaker(10, 15*time.Second),
		telemetry:  tel,
		storage:    store,
		stopCh:     make(chan struct{}),
		rnd:        rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	for i := 0; i < cfg.WorkerCount; i++ {
		d.wg.Add(1)
		go d.workerLoop()
	}

	return d, nil
}

// Enqueue submits a captured transaction for background shadowing.
// It returns immediately to guarantee zero performance degradation on primary traffic.
func (d *ShadowDispatcher) Enqueue(task ShadowTask) {
	effectiveRate := task.SamplingRate
	if effectiveRate <= 0 {
		effectiveRate = d.config.SampleRate
	}

	// 1. Sampling filter
	if effectiveRate < 1.0 {
		d.rndMu.Lock()
		r := d.rnd.Float64()
		d.rndMu.Unlock()
		if r > effectiveRate {
			return
		}
	}

	// 2. Mutation safety policy check
	if !d.config.MutationConfig.ShouldShadow(task.Request.Method) {
		return
	}

	// 3. Circuit breaker check
	if !d.circuit.Allow() {
		d.telemetry.RecordDropped()
		return
	}

	// 4. Non-blocking queue insert
	select {
	case d.queue <- task:
		d.telemetry.SetQueueDepth(int64(len(d.queue)))
	default:
		// Queue full during traffic surge: shed shadow request safely
		d.telemetry.RecordDropped()
	}
}

// workerLoop processes shadow tasks asynchronously.
func (d *ShadowDispatcher) workerLoop() {
	defer d.wg.Done()

	for {
		select {
		case <-d.stopCh:
			return
		case task, ok := <-d.queue:
			if !ok {
				return
			}
			d.telemetry.SetQueueDepth(int64(len(d.queue)))
			d.processTask(task)
		}
	}
}

// processTask executes the request to the shadow target and performs semantic diffing.
func (d *ShadowDispatcher) processTask(task ShadowTask) {
	cloned := task.Request

	// Sanitize body and headers for PII protection
	sanitizedBody := security.SanitizeBody(cloned.Body, d.config.PIIConfig)
	sanitizedHeaders := security.SanitizeHeaders(cloned.Headers, d.config.PIIConfig)

	targetURL := *d.shadowURL
	targetURL.Path = cloned.URLPath
	targetURL.RawQuery = cloned.Query

	ctx, cancel := context.WithTimeout(context.Background(), d.config.RequestTimeout)
	defer cancel()

	var reqBody io.Reader
	if len(sanitizedBody) > 0 {
		reqBody = bytes.NewReader(sanitizedBody)
	}

	shadowReq, err := http.NewRequestWithContext(ctx, cloned.Method, targetURL.String(), reqBody)
	if err != nil {
		d.circuit.RecordFailure()
		return
	}

	shadowReq.Header = sanitizedHeaders
	// Strip hop-by-hop headers
	shadowReq.Header.Del("Connection")
	shadowReq.Header.Del("Keep-Alive")
	shadowReq.Header.Del("Proxy-Authenticate")
	shadowReq.Header.Del("Proxy-Authorization")
	shadowReq.Header.Del("Te")
	shadowReq.Header.Del("Trailers")
	shadowReq.Header.Del("Transfer-Encoding")
	shadowReq.Header.Del("Upgrade")

	// Apply shadow headers overrides
	for k, v := range d.config.ShadowHeaders {
		shadowReq.Header.Set(k, v)
	}

	// Apply mutation transforms (e.g. X-Dry-Run: true)
	d.config.MutationConfig.ApplySafetyTransforms(shadowReq)

	// Execute call and record shadow latency
	start := time.Now()
	resp, respErr := d.httpClient.Do(shadowReq)
	duration := time.Since(start)

	var shadowSnapshot diff.ResponseSnapshot
	if respErr != nil {
		d.circuit.RecordFailure()
		shadowSnapshot = diff.NewResponseSnapshot(0, http.Header{}, "", duration, respErr)
	} else {
		defer resp.Body.Close()
		bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
		if readErr != nil {
			d.circuit.RecordFailure()
			shadowSnapshot = diff.NewResponseSnapshot(resp.StatusCode, resp.Header, "", duration, readErr)
		} else {
			if resp.StatusCode >= 500 {
				d.circuit.RecordFailure()
			} else {
				d.circuit.RecordSuccess()
			}
			shadowSnapshot = diff.NewResponseSnapshot(resp.StatusCode, resp.Header, string(bodyBytes), duration, nil)
		}
	}

	// 5. Semantic Comparison
	diffResult := diff.CompareSnapshots(task.PrimarySnapshot, shadowSnapshot, d.config.MaskConfig, d.config.CompareOptions)

	pair := diff.ShadowPair{
		ID:             task.ID,
		Timestamp:      task.Timestamp,
		Method:         cloned.Method,
		Path:           cloned.URLPath,
		Query:          cloned.Query,
		RequestHeaders: sanitizedHeaders,
		RequestBody:    string(sanitizedBody),
		Primary:        task.PrimarySnapshot,
		Shadow:         shadowSnapshot,
		Diff:           diffResult,
	}

	// 6. Record telemetry & save to storage
	d.storage.Push(pair)
	d.telemetry.RecordTransaction(pair)

	// 7. Optional offline traffic recording
	if d.config.Recorder != nil {
		d.config.Recorder.Record(pair)
	}

	// 8. Optional webhook alerting
	if d.config.AlertManager != nil {
		d.config.AlertManager.CheckAndAlert(pair, d.telemetry.OverallSRR())
	}
}

// Stop shuts down the worker pool gracefully.
func (d *ShadowDispatcher) Stop() {
	close(d.stopCh)
	d.wg.Wait()
	if d.config.Recorder != nil {
		_ = d.config.Recorder.Close()
	}
}
