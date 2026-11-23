package diff

import (
	"net/http"
	"time"
)

// MismatchKind categorizes the nature of a detected regression.
type MismatchKind string

const (
	MismatchKindTypeDrift        MismatchKind = "TYPE_DRIFT"
	MismatchKindMissingKey       MismatchKind = "MISSING_KEY"
	MismatchKindUnexpectedKey    MismatchKind = "UNEXPECTED_KEY"
	MismatchKindValueDrift       MismatchKind = "VALUE_DRIFT"
	MismatchKindStatusDrift      MismatchKind = "STATUS_DRIFT"
	MismatchKindArrayLengthDrift MismatchKind = "ARRAY_LENGTH_DRIFT"
	MismatchKindHeaderDrift      MismatchKind = "HEADER_DRIFT"
)

// Mismatch holds specific discrepancy details for a given key path.
type Mismatch struct {
	Path         string       `json:"path"`
	Kind         MismatchKind `json:"kind"`
	PrimaryValue any          `json:"primary_value"`
	PrimaryType  string       `json:"primary_type"`
	ShadowValue  any          `json:"shadow_value"`
	ShadowType   string       `json:"shadow_type"`
	Message      string       `json:"message"`
}

// ResponseSnapshot contains the captured response attributes of a service call.
type ResponseSnapshot struct {
	StatusCode int           `json:"status_code"`
	Headers    http.Header   `json:"headers"`
	Body       string        `json:"body"`
	Latency    time.Duration `json:"latency_ns"`
	LatencyMs  float64       `json:"latency_ms"`
	Error      string        `json:"error,omitempty"`
}

// DiffResult encapsulates the complete semantic comparison between primary and shadow responses.
type DiffResult struct {
	IsMatch          bool          `json:"is_match"`
	SemanticDistance float64       `json:"semantic_distance"`
	StatusDrift      bool          `json:"status_drift"`
	Mismatches       []Mismatch    `json:"mismatches"`
	PrimaryCleanJSON string        `json:"primary_clean_json,omitempty"`
	ShadowCleanJSON  string        `json:"shadow_clean_json,omitempty"`
	LatencyDelta     time.Duration `json:"latency_delta_ns"`
	LatencyDeltaMs   float64       `json:"latency_delta_ms"`
}

// ShadowPair models an observed transaction pair (inbound request, primary response, shadow response).
type ShadowPair struct {
	ID             string           `json:"id"`
	Timestamp      time.Time        `json:"timestamp"`
	Method         string           `json:"method"`
	Path           string           `json:"path"`
	Query          string           `json:"query"`
	RequestHeaders http.Header      `json:"request_headers"`
	RequestBody    string           `json:"request_body"`
	Primary        ResponseSnapshot `json:"primary"`
	Shadow         ResponseSnapshot `json:"shadow"`
	Diff           DiffResult       `json:"diff"`
}

// RouteScore aggregates regression performance for a particular HTTP path.
type RouteScore struct {
	Route              string  `json:"route"`
	TotalRequests      int64   `json:"total_requests"`
	MatchedRequests    int64   `json:"matched_requests"`
	RegressedRequests  int64   `json:"regressed_requests"`
	SRR                float64 `json:"srr"` // Semantic Regression Rate (0.0 to 1.0)
	PrimaryLatencyP50Ms float64 `json:"primary_latency_p50_ms"`
	PrimaryLatencyP99Ms float64 `json:"primary_latency_p99_ms"`
	ShadowLatencyP50Ms  float64 `json:"shadow_latency_p50_ms"`
	ShadowLatencyP99Ms  float64 `json:"shadow_latency_p99_ms"`
	LatencyDeltaP99Ms  float64 `json:"latency_delta_p99_ms"`
}
