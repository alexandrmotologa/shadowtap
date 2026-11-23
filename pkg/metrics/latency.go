package metrics

import (
	"math"
	"sort"
	"sync"
	"time"
)

// PercentileTracker records durations and calculates p50, p90, p99 percentiles efficiently.
// Uses a fixed-capacity ring buffer to maintain bounded memory.
type PercentileTracker struct {
	mu       sync.RWMutex
	samples  []float64 // stored in milliseconds
	capacity int
	index    int
	isFull   bool
}

// NewPercentileTracker creates a tracker with a designated sample capacity.
func NewPercentileTracker(capacity int) *PercentileTracker {
	if capacity <= 0 {
		capacity = 1000
	}
	return &PercentileTracker{
		samples:  make([]float64, capacity),
		capacity: capacity,
	}
}

// Observe adds a new duration observation.
func (p *PercentileTracker) Observe(d time.Duration) {
	ms := float64(d.Microseconds()) / 1000.0
	p.mu.Lock()
	p.samples[p.index] = ms
	p.index++
	if p.index >= p.capacity {
		p.index = 0
		p.isFull = true
	}
	p.mu.Unlock()
}

// Percentiles calculates p50, p90, and p99 in milliseconds.
func (p *PercentileTracker) Percentiles() (p50, p90, p99 float64) {
	p.mu.RLock()
	count := p.index
	if p.isFull {
		count = p.capacity
	}
	if count == 0 {
		p.mu.RUnlock()
		return 0, 0, 0
	}

	sorted := make([]float64, count)
	copy(sorted, p.samples[:count])
	p.mu.RUnlock()

	sort.Float64s(sorted)

	p50 = percentile(sorted, 0.50)
	p90 = percentile(sorted, 0.90)
	p99 = percentile(sorted, 0.99)
	return p50, p90, p99
}

func percentile(sorted []float64, q float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	rank := q * float64(n-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	weight := rank - float64(lower)

	if lower == upper {
		return sorted[lower]
	}
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}
