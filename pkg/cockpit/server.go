package cockpit

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexandrmotologa/shadowtap/pkg/audit"
	"github.com/alexandrmotologa/shadowtap/pkg/diff"
	"github.com/alexandrmotologa/shadowtap/pkg/metrics"
	"github.com/alexandrmotologa/shadowtap/pkg/storage"
)

//go:embed web/*
var embeddedWebFS embed.FS

// ServerConfig configures the Cockpit web studio.
type ServerConfig struct {
	Port int
}

// Server serves the embedded real-time web cockpit and REST telemetry API.
type Server struct {
	config     ServerConfig
	telemetry  *metrics.GlobalTelemetry
	storage    *storage.RingBuffer
	maskConfig *diff.MaskConfig
	httpServer *http.Server
}

// NewServer builds the Cockpit server.
func NewServer(cfg ServerConfig, tel *metrics.GlobalTelemetry, store *storage.RingBuffer, mask *diff.MaskConfig) *Server {
	if cfg.Port <= 0 {
		cfg.Port = 8989
	}
	return &Server{
		config:     cfg,
		telemetry:  tel,
		storage:    store,
		maskConfig: mask,
	}
}

// Start launches the Cockpit dashboard HTTP server.
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/routes", s.handleRoutes)
	mux.HandleFunc("/api/pairs", s.handlePairs)
	mux.HandleFunc("/api/events/live", s.handleLiveEvents)
	mux.HandleFunc("/api/export/ndjson", s.handleExportNDJSON)
	mux.HandleFunc("/api/mask/ignore", s.handleMaskIgnore)
	mux.HandleFunc("/api/mask", s.handleGetMask)
	mux.HandleFunc("/metrics", s.handleMetrics)

	// Static frontend assets
	subFS, err := fs.Sub(embeddedWebFS, "web")
	if err != nil {
		return fmt.Errorf("failed to mount embedded web fs: %w", err)
	}
	fileServer := http.FileServer(http.FS(subFS))
	mux.Handle("/", fileServer)

	addr := fmt.Sprintf(":%d", s.config.Port)
	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      corsMiddleware(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // Zero write timeout for SSE streams
		IdleTimeout:  60 * time.Second,
	}

	return s.httpServer.ListenAndServe()
}

// Shutdown stops the cockpit server gracefully.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	stats := s.telemetry.Stats()
	_ = json.NewEncoder(w).Encode(stats)
}

func (s *Server) handleRoutes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	routes := s.telemetry.GetRouteScores()
	_ = json.NewEncoder(w).Encode(routes)
}

func (s *Server) handlePairs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	q := r.URL.Query()

	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 50
	}

	filter := storage.QueryFilter{
		Route:         q.Get("route"),
		OnlyRegressed: q.Get("only_regressed") == "true",
		Search:        q.Get("search"),
		Limit:         limit,
	}

	pairs := s.storage.Query(filter)
	_ = json.NewEncoder(w).Encode(pairs)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte(s.telemetry.FormatPrometheus()))
}

// handleExportNDJSON streams recorded transaction pairs as a downloadable NDJSON file for CI/CD audits.
func (s *Server) handleExportNDJSON(w http.ResponseWriter, r *http.Request) {
	pairs := s.storage.Query(storage.QueryFilter{Limit: 5000})
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", "attachment; filename=\"shadowtap_traffic.ndjson\"")

	for _, p := range pairs {
		headersMap := make(map[string]string)
		for k, v := range p.RequestHeaders {
			if len(v) > 0 {
				headersMap[k] = v[0]
			}
		}
		entry := audit.RecordedEntry{
			ID:             p.ID,
			Timestamp:      p.Timestamp.Format(time.RFC3339),
			Method:         p.Method,
			Path:           p.Path,
			Query:          p.Query,
			RequestHeaders: headersMap,
			RequestBody:    p.RequestBody,
			ExpectedStatus: p.Primary.StatusCode,
			ExpectedBody:   p.Primary.Body,
		}
		data, err := json.Marshal(entry)
		if err == nil {
			_, _ = w.Write(append(data, '\n'))
		}
	}
}

// handleMaskIgnore registers a new ignore pattern dynamically at runtime without restarting.
func (s *Server) handleMaskIgnore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		http.Error(w, "Invalid path payload", http.StatusBadRequest)
		return
	}

	if s.maskConfig != nil {
		s.maskConfig.AddIgnoredPath(req.Path)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":       true,
		"added_path":    req.Path,
		"ignored_paths": s.maskConfig.GetIgnoredPaths(),
	})
}

func (s *Server) handleGetMask(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var paths []string
	if s.maskConfig != nil {
		paths = s.maskConfig.GetIgnoredPaths()
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ignored_paths": paths,
	})
}

// handleLiveEvents pushes real-time diff events to the cockpit via Server-Sent Events (SSE).
func (s *Server) handleLiveEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := s.storage.Subscribe()
	defer s.storage.Unsubscribe(ch)

	// Send initial ping to establish connection
	_, _ = fmt.Fprintf(w, "event: ping\ndata: connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, _ = fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case pair, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(pair)
			if err == nil {
				cleanData := strings.ReplaceAll(string(data), "\n", " ")
				_, _ = fmt.Fprintf(w, "data: %s\n\n", cleanData)
				flusher.Flush()
			}
		}
	}
}
