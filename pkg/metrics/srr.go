package metrics

import (
	"sync"
	"sync/atomic"

	"github.com/alexandrmotologa/shadowtap/pkg/diff"
)

// RouteTracker maintains real-time regression statistics and latency profiles for a single route.
type RouteTracker struct {
	Route           string
	TotalRequests   atomic.Int64
	MatchedRequests atomic.Int64
	RegressedCount  atomic.Int64
	StatusDriftCount atomic.Int64
	PrimaryLatency  *PercentileTracker
	ShadowLatency   *PercentileTracker
}

func newRouteTracker(route string) *RouteTracker {
	return &RouteTracker{
		Route:          route,
		PrimaryLatency: NewPercentileTracker(2000),
		ShadowLatency:  NewPercentileTracker(2000),
	}
}

// GlobalTelemetry aggregates metrics across all routes.
type GlobalTelemetry struct {
	mu           sync.RWMutex
	routes       map[string]*RouteTracker
	totalShadow  atomic.Int64
	totalRegress atomic.Int64
	totalDropped atomic.Int64
	queueDepth   atomic.Int64
}

// NewGlobalTelemetry initializes a new telemetry coordinator.
func NewGlobalTelemetry() *GlobalTelemetry {
	return &GlobalTelemetry{
		routes: make(map[string]*RouteTracker),
	}
}

// GetOrCreateRoute gets or creates a tracker for a specific path.
func (gt *GlobalTelemetry) GetOrCreateRoute(route string) *RouteTracker {
	if route == "" {
		route = "/"
	}
	gt.mu.RLock()
	tracker, exists := gt.routes[route]
	gt.mu.RUnlock()
	if exists {
		return tracker
	}

	gt.mu.Lock()
	defer gt.mu.Unlock()
	if tracker, exists := gt.routes[route]; exists {
		return tracker
	}
	tracker = newRouteTracker(route)
	gt.routes[route] = tracker
	return tracker
}

// RecordTransaction updates global and route-specific counters based on diff results.
func (gt *GlobalTelemetry) RecordTransaction(pair diff.ShadowPair) {
	gt.totalShadow.Add(1)
	if !pair.Diff.IsMatch {
		gt.totalRegress.Add(1)
	}

	rt := gt.GetOrCreateRoute(pair.Path)
	rt.TotalRequests.Add(1)
	if pair.Diff.IsMatch {
		rt.MatchedRequests.Add(1)
	} else {
		rt.RegressedCount.Add(1)
	}
	if pair.Diff.StatusDrift {
		rt.StatusDriftCount.Add(1)
	}

	rt.PrimaryLatency.Observe(pair.Primary.Latency)
	rt.ShadowLatency.Observe(pair.Shadow.Latency)
}

// RecordDropped increments the dropped shadow request counter.
func (gt *GlobalTelemetry) RecordDropped() {
	gt.totalDropped.Add(1)
}

// SetQueueDepth updates current queue buffer depth.
func (gt *GlobalTelemetry) SetQueueDepth(depth int64) {
	gt.queueDepth.Store(depth)
}

// OverallSRR returns the global Semantic Regression Rate (0.0 to 1.0).
func (gt *GlobalTelemetry) OverallSRR() float64 {
	tot := gt.totalShadow.Load()
	if tot == 0 {
		return 0.0
	}
	reg := gt.totalRegress.Load()
	return float64(reg) / float64(tot)
}

// GetRouteScores generates a list of RouteScore summaries for UI and scorecards.
func (gt *GlobalTelemetry) GetRouteScores() []diff.RouteScore {
	gt.mu.RLock()
	routes := make([]*RouteTracker, 0, len(gt.routes))
	for _, r := range gt.routes {
		routes = append(routes, r)
	}
	gt.mu.RUnlock()

	scores := make([]diff.RouteScore, 0, len(routes))
	for _, r := range routes {
		tot := r.TotalRequests.Load()
		reg := r.RegressedCount.Load()
		matched := r.MatchedRequests.Load()

		srr := 0.0
		if tot > 0 {
			srr = float64(reg) / float64(tot)
		}

		primP50, _, primP99 := r.PrimaryLatency.Percentiles()
		shadP50, _, shadP99 := r.ShadowLatency.Percentiles()

		deltaP99 := shadP99 - primP99

		scores = append(scores, diff.RouteScore{
			Route:               r.Route,
			TotalRequests:       tot,
			MatchedRequests:     matched,
			RegressedRequests:   reg,
			SRR:                 srr,
			PrimaryLatencyP50Ms: primP50,
			PrimaryLatencyP99Ms: primP99,
			ShadowLatencyP50Ms:  shadP50,
			ShadowLatencyP99Ms:  shadP99,
			LatencyDeltaP99Ms:   deltaP99,
		})
	}
	return scores
}

// SnapshotStats returns a summary struct for metrics reporting.
type SnapshotStats struct {
	TotalShadowed int64   `json:"total_shadowed"`
	TotalRegressed int64  `json:"total_regressed"`
	TotalDropped  int64   `json:"total_dropped"`
	QueueDepth    int64   `json:"queue_depth"`
	GlobalSRR     float64 `json:"global_srr"`
}

// Stats returns high-level counts.
func (gt *GlobalTelemetry) Stats() SnapshotStats {
	return SnapshotStats{
		TotalShadowed:  gt.totalShadow.Load(),
		TotalRegressed: gt.totalRegress.Load(),
		TotalDropped:   gt.totalDropped.Load(),
		QueueDepth:     gt.queueDepth.Load(),
		GlobalSRR:      gt.OverallSRR(),
	}
}
