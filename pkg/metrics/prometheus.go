package metrics

import (
	"fmt"
	"strings"
)

// FormatPrometheus exports telemetry stats in standard Prometheus text format.
func (gt *GlobalTelemetry) FormatPrometheus() string {
	var sb strings.Builder

	stats := gt.Stats()

	sb.WriteString("# HELP shadowtap_shadowed_total Total number of shadowed requests processed.\n")
	sb.WriteString("# TYPE shadowtap_shadowed_total counter\n")
	sb.WriteString(fmt.Sprintf("shadowtap_shadowed_total %d\n\n", stats.TotalShadowed))

	sb.WriteString("# HELP shadowtap_regressions_total Total number of detected semantic regressions.\n")
	sb.WriteString("# TYPE shadowtap_regressions_total counter\n")
	sb.WriteString(fmt.Sprintf("shadowtap_regressions_total %d\n\n", stats.TotalRegressed))

	sb.WriteString("# HELP shadowtap_dropped_total Total number of shadow requests dropped due to buffer saturation.\n")
	sb.WriteString("# TYPE shadowtap_dropped_total counter\n")
	sb.WriteString(fmt.Sprintf("shadowtap_dropped_total %d\n\n", stats.TotalDropped))

	sb.WriteString("# HELP shadowtap_queue_depth Current number of pending shadow requests in queue.\n")
	sb.WriteString("# TYPE shadowtap_queue_depth gauge\n")
	sb.WriteString(fmt.Sprintf("shadowtap_queue_depth %d\n\n", stats.QueueDepth))

	sb.WriteString("# HELP shadowtap_global_srr Global Semantic Regression Rate ratio (0.0 to 1.0).\n")
	sb.WriteString("# TYPE shadowtap_global_srr gauge\n")
	sb.WriteString(fmt.Sprintf("shadowtap_global_srr %.6f\n\n", stats.GlobalSRR))

	// Per-route breakdown
	scores := gt.GetRouteScores()
	if len(scores) > 0 {
		sb.WriteString("# HELP shadowtap_route_requests_total Total requests processed per route.\n")
		sb.WriteString("# TYPE shadowtap_route_requests_total counter\n")
		for _, s := range scores {
			sb.WriteString(fmt.Sprintf("shadowtap_route_requests_total{route=%q} %d\n", s.Route, s.TotalRequests))
		}
		sb.WriteString("\n")

		sb.WriteString("# HELP shadowtap_route_srr_ratio Semantic Regression Rate per route.\n")
		sb.WriteString("# TYPE shadowtap_route_srr_ratio gauge\n")
		for _, s := range scores {
			sb.WriteString(fmt.Sprintf("shadowtap_route_srr_ratio{route=%q} %.6f\n", s.Route, s.SRR))
		}
		sb.WriteString("\n")

		sb.WriteString("# HELP shadowtap_route_latency_p99_ms 99th percentile latency in milliseconds.\n")
		sb.WriteString("# TYPE shadowtap_route_latency_p99_ms gauge\n")
		for _, s := range scores {
			sb.WriteString(fmt.Sprintf("shadowtap_route_latency_p99_ms{route=%q,service=\"primary\"} %.3f\n", s.Route, s.PrimaryLatencyP99Ms))
			sb.WriteString(fmt.Sprintf("shadowtap_route_latency_p99_ms{route=%q,service=\"shadow\"} %.3f\n", s.Route, s.ShadowLatencyP99Ms))
		}
	}

	return sb.String()
}
