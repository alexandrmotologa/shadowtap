package proxy

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/alexandrmotologa/shadowtap/pkg/diff"
)

// ProxyServerConfig holds parameters for the primary reverse proxy and shadowing engine.
type ProxyServerConfig struct {
	ListenAddr    string
	PrimaryTarget string
	MaxBodyBytes  int64
}

// responseCapture is an http.ResponseWriter that captures status code and body for diffing.
type responseCapture struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
	written    bool
}

func (rc *responseCapture) WriteHeader(code int) {
	if !rc.written {
		rc.statusCode = code
		rc.ResponseWriter.WriteHeader(code)
		rc.written = true
	}
}

func (rc *responseCapture) Write(b []byte) (int, error) {
	if !rc.written {
		rc.statusCode = http.StatusOK
		rc.written = true
	}
	rc.body.Write(b)
	return rc.ResponseWriter.Write(b)
}

func (rc *responseCapture) Flush() {
	if f, ok := rc.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Server is the high-performance reverse proxy that mirrors traffic to shadow candidates.
type Server struct {
	config       ProxyServerConfig
	primaryURL   *url.URL
	reverseProxy *httputil.ReverseProxy
	dispatcher   *ShadowDispatcher
	httpServer   *http.Server
	txCounter    atomic.Uint64
}

// NewServer initializes the proxy engine connected to primary and shadow dispatcher.
func NewServer(cfg ProxyServerConfig, dispatcher *ShadowDispatcher) (*Server, error) {
	primaryURL, err := url.Parse(cfg.PrimaryTarget)
	if err != nil {
		return nil, fmt.Errorf("invalid primary target url %q: %w", cfg.PrimaryTarget, err)
	}

	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 10 * 1024 * 1024 // 10MB
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
	}

	rp := httputil.NewSingleHostReverseProxy(primaryURL)
	rp.Transport = transport

	// Custom error handler if primary fails
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, fmt.Sprintf("primary upstream error: %v", err), http.StatusBadGateway)
	}

	s := &Server{
		config:       cfg,
		primaryURL:   primaryURL,
		reverseProxy: rp,
		dispatcher:   dispatcher,
	}

	return s, nil
}

// ServeHTTP handles every inbound request with zero-impact asynchronous shadowing.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	txID := "tx-" + strconv.FormatUint(s.txCounter.Add(1), 10)
	arrival := time.Now()

	// 1. Clone inbound request asynchronously before reading stream for primary
	clonedReq, cloneErr := CloneInboundRequest(r, s.config.MaxBodyBytes)
	if cloneErr != nil {
		// Log and continue proxying to primary
		clonedReq = &ClonedRequest{
			Method:  r.Method,
			URLPath: r.URL.Path,
			Query:   r.URL.RawQuery,
			Headers: r.Header.Clone(),
			Host:    r.Host,
		}
	}
	clonedReq.ID = txID

	// 2. Wrap ResponseWriter to capture primary status code and body
	capture := &responseCapture{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
	}

	// 3. Forward request to Primary synchronously
	primStart := time.Now()
	s.reverseProxy.ServeHTTP(capture, r)
	primLatency := time.Since(primStart)

	// 4. Capture Primary response snapshot
	primarySnapshot := diff.ResponseSnapshot{
		StatusCode: capture.statusCode,
		Headers:    capture.Header().Clone(),
		Body:       capture.body.String(),
		Latency:    primLatency,
		LatencyMs:  float64(primLatency.Microseconds()) / 1000.0,
	}

	// 5. Enqueue shadow task asynchronously (non-blocking)
	if s.dispatcher != nil {
		s.dispatcher.Enqueue(ShadowTask{
			ID:              txID,
			Timestamp:       arrival,
			Request:         clonedReq,
			PrimarySnapshot: primarySnapshot,
		})
	}
}

// Start launches the HTTP listener.
func (s *Server) Start() error {
	s.httpServer = &http.Server{
		Addr:         s.config.ListenAddr,
		Handler:      s,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully stops the proxy.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}
