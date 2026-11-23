package diff

import (
	"path"
	"regexp"
	"strings"
	"sync"
)

// Standard volatile patterns detected in dynamic microservices.
var (
	// Matches ISO 8601 / RFC 3339 timestamps (e.g., 2026-11-23T15:30:00.123Z).
	regexISO8601 = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})?$`)

	// Matches standard UUIDs (v1 through v5).
	regexUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

	// Matches JSON Web Tokens (JWT).
	regexJWT = regexp.MustCompile(`^ey[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}$`)

	// Matches 16, 32 or 64 character hex trace identifiers (e.g., W3C traceparent / OpenTelemetry).
	regexTraceHex = regexp.MustCompile(`^[0-9a-fA-F]{16}$|^[0-9a-fA-F]{32}$|^[0-9a-fA-F]{64}$`)
)

// MaskConfig governs which paths and dynamic values are treated as transient noise.
type MaskConfig struct {
	mu sync.RWMutex
	// IgnoredPaths contains glob patterns or exact path names (e.g. "*.timestamp", "metadata.requestId").
	IgnoredPaths []string
	// IgnoreTimestamps removes values matching RFC 3339 timestamps.
	IgnoreTimestamps bool
	// IgnoreUUIDs removes values matching UUID v4/v5 patterns.
	IgnoreUUIDs bool
	// IgnoreJWTs removes values matching JWT token formats.
	IgnoreJWTs bool
	// IgnoreTraceIDs removes values matching hexadecimal trace/span IDs.
	IgnoreTraceIDs bool
	// CustomRegexes allows user-supplied regular expressions for value masking.
	CustomRegexes []*regexp.Regexp
}

// DefaultMaskConfig returns standard noise-reduction defaults for production microservices.
func DefaultMaskConfig() MaskConfig {
	return MaskConfig{
		IgnoredPaths: []string{
			"*.timestamp",
			"*_timestamp",
			"*.created_at",
			"*.updated_at",
			"*.createdAt",
			"*.updatedAt",
			"*.requestId",
			"*.request_id",
			"*.traceId",
			"*.trace_id",
			"*.spanId",
			"*.span_id",
		},
		IgnoreTimestamps: true,
		IgnoreUUIDs:      true,
		IgnoreJWTs:       true,
		IgnoreTraceIDs:   true,
		CustomRegexes:    nil,
	}
}

// AddIgnoredPath thread-safely appends a new path pattern to the active ignore rules.
func (c *MaskConfig) AddIgnoredPath(p string) bool {
	if p == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	pLower := strings.ToLower(p)
	for _, existing := range c.IgnoredPaths {
		if strings.ToLower(existing) == pLower {
			return false // Already present
		}
	}
	c.IgnoredPaths = append(c.IgnoredPaths, p)
	return true
}

// GetIgnoredPaths returns a snapshot slice of currently active ignored patterns.
func (c *MaskConfig) GetIgnoredPaths() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	res := make([]string, len(c.IgnoredPaths))
	copy(res, c.IgnoredPaths)
	return res
}

// IsPathIgnored checks if a given JSON path matches any configured ignored patterns.
func (c *MaskConfig) IsPathIgnored(p string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	pLower := strings.ToLower(p)

	for _, pattern := range c.IgnoredPaths {
		patLower := strings.ToLower(pattern)
		// Exact match
		if pLower == patLower {
			return true
		}
		// Glob match via path.Match
		if matched, err := path.Match(patLower, pLower); err == nil && matched {
			return true
		}
		// Suffix match for field name after the last dot
		if strings.HasPrefix(patLower, "*.") {
			suffix := strings.TrimPrefix(patLower, "*")
			if strings.HasSuffix(pLower, suffix) {
				return true
			}
		}
		// Substring match if wildcard at ends
		if strings.HasPrefix(patLower, "*") && strings.HasSuffix(patLower, "*") {
			sub := strings.Trim(patLower, "*")
			if strings.Contains(pLower, sub) {
				return true
			}
		}
	}
	return false
}

// IsValueVolatile checks if a string value represents non-deterministic runtime noise.
func (c *MaskConfig) IsValueVolatile(valStr string) bool {
	if valStr == "" {
		return false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.IgnoreTimestamps && regexISO8601.MatchString(valStr) {
		return true
	}
	if c.IgnoreUUIDs && regexUUID.MatchString(valStr) {
		return true
	}
	if c.IgnoreJWTs && regexJWT.MatchString(valStr) {
		return true
	}
	if c.IgnoreTraceIDs && regexTraceHex.MatchString(valStr) {
		return true
	}

	for _, re := range c.CustomRegexes {
		if re.MatchString(valStr) {
			return true
		}
	}

	return false
}
