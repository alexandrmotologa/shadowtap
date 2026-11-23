package storage

import (
	"strings"
	"sync"

	"github.com/alexandrmotologa/shadowtap/pkg/diff"
)

// RingBuffer stores a circular bounded slice of ShadowPair records and broadcasts live events.
type RingBuffer struct {
	mu          sync.RWMutex
	items       []diff.ShadowPair
	capacity    int
	head        int
	count       int
	subscribers map[chan diff.ShadowPair]struct{}
	subMu       sync.RWMutex
}

// NewRingBuffer allocates an in-memory ring buffer with fixed capacity.
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = 1000
	}
	return &RingBuffer{
		items:       make([]diff.ShadowPair, capacity),
		capacity:    capacity,
		subscribers: make(map[chan diff.ShadowPair]struct{}),
	}
}

// Push appends a new transaction pair, overwriting the oldest entry when full, and notifies subscribers.
func (rb *RingBuffer) Push(pair diff.ShadowPair) {
	rb.mu.Lock()
	rb.items[rb.head] = pair
	rb.head = (rb.head + 1) % rb.capacity
	if rb.count < rb.capacity {
		rb.count++
	}
	rb.mu.Unlock()

	// Broadcast non-blocking to active live SSE subscribers
	rb.subMu.RLock()
	for ch := range rb.subscribers {
		select {
		case ch <- pair:
		default:
			// Drop if subscriber is slow to keep proxy non-blocking
		}
	}
	rb.subMu.RUnlock()
}

// QueryFilter specifies filter criteria for retrieving historical pairs.
type QueryFilter struct {
	Route        string
	OnlyRegressed bool
	Search       string
	Limit        int
}

// Query returns pairs matching filter criteria ordered newest first.
func (rb *RingBuffer) Query(filter QueryFilter) []diff.ShadowPair {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.count == 0 {
		return nil
	}

	limit := filter.Limit
	if limit <= 0 || limit > rb.count {
		limit = rb.count
	}

	results := make([]diff.ShadowPair, 0, limit)
	searchLower := strings.ToLower(filter.Search)

	// Traverse from newest to oldest
	for i := 0; i < rb.count; i++ {
		idx := (rb.head - 1 - i + rb.capacity) % rb.capacity
		item := rb.items[idx]

		if filter.OnlyRegressed && item.Diff.IsMatch {
			continue
		}

		if filter.Route != "" && item.Path != filter.Route {
			continue
		}

		if searchLower != "" {
			match := strings.Contains(strings.ToLower(item.Path), searchLower) ||
				strings.Contains(strings.ToLower(item.Method), searchLower) ||
				strings.Contains(strings.ToLower(item.ID), searchLower)
			if !match {
				continue
			}
		}

		results = append(results, item)
		if len(results) >= limit {
			break
		}
	}

	return results
}

// Subscribe attaches a channel that receives live diff events.
func (rb *RingBuffer) Subscribe() chan diff.ShadowPair {
	ch := make(chan diff.ShadowPair, 100)
	rb.subMu.Lock()
	rb.subscribers[ch] = struct{}{}
	rb.subMu.Unlock()
	return ch
}

// Unsubscribe detaches a previously registered channel.
func (rb *RingBuffer) Unsubscribe(ch chan diff.ShadowPair) {
	rb.subMu.Lock()
	delete(rb.subscribers, ch)
	rb.subMu.Unlock()
	close(ch)
}
