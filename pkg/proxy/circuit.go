package proxy

import (
	"sync"
	"time"
)

// CircuitState represents the health status of the shadow candidate target.
type CircuitState int

const (
	StateClosed CircuitState = iota // Normal operation: shadow requests forwarded
	StateHalfOpen                   // Trial mode: test if candidate recovered
	StateOpen                       // Candidate failing: shed shadow traffic
)

// CircuitBreaker guards the shadow dispatcher against failing downstream targets.
type CircuitBreaker struct {
	mu               sync.RWMutex
	state            CircuitState
	failureThreshold int
	consecutiveFails int
	cooldown         time.Duration
	lastStateChange  time.Time
}

// NewCircuitBreaker creates a breaker with designated threshold and cooldown.
func NewCircuitBreaker(threshold int, cooldown time.Duration) *CircuitBreaker {
	if threshold <= 0 {
		threshold = 10
	}
	if cooldown <= 0 {
		cooldown = 15 * time.Second
	}
	return &CircuitBreaker{
		state:            StateClosed,
		failureThreshold: threshold,
		cooldown:         cooldown,
		lastStateChange:  time.Now(),
	}
}

// Allow returns true if a shadow request may be dispatched.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()
	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		if now.Sub(cb.lastStateChange) > cb.cooldown {
			cb.state = StateHalfOpen
			cb.lastStateChange = now
			return true
		}
		return false
	case StateHalfOpen:
		return true
	default:
		return true
	}
}

// RecordSuccess marks a successful shadow response, resetting the breaker.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFails = 0
	if cb.state != StateClosed {
		cb.state = StateClosed
		cb.lastStateChange = time.Now()
	}
}

// RecordFailure marks a failed shadow response, tripping the breaker if threshold is reached.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFails++
	if cb.consecutiveFails >= cb.failureThreshold {
		cb.state = StateOpen
		cb.lastStateChange = time.Now()
	}
}

// State returns current circuit status.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}
