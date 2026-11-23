package security

import (
	"net/http"
	"strings"
)

// MutationPolicy defines how state-altering HTTP methods are safely handled in shadow requests.
type MutationPolicy string

const (
	// PolicyDryRunHeader injects X-Dry-Run: true and X-Shadow-Request: true into the shadow call.
	PolicyDryRunHeader MutationPolicy = "DRY_RUN_HEADER"

	// PolicyPathPrefix prepends a sandbox prefix (e.g., /sandbox) to the request path.
	PolicyPathPrefix MutationPolicy = "PATH_PREFIX"

	// PolicyDropMutations completely skips shadowing for mutating requests (POST, PUT, PATCH, DELETE).
	PolicyDropMutations MutationPolicy = "DROP_MUTATIONS"

	// PolicyPassthrough passes mutations unchanged (use only in isolated sandbox environments).
	PolicyPassthrough MutationPolicy = "PASSTHROUGH"
)

// MutationConfig configures mutation protection behavior.
type MutationConfig struct {
	Policy         MutationPolicy
	DryRunHeader   string // e.g. "X-Dry-Run"
	DryRunValue    string // e.g. "true"
	PathPrefix     string // e.g. "/sandbox"
	CustomHeaders  map[string]string
}

// DefaultMutationConfig returns safe defaults with dry-run header injection.
func DefaultMutationConfig() MutationConfig {
	return MutationConfig{
		Policy:       PolicyDryRunHeader,
		DryRunHeader: "X-Dry-Run",
		DryRunValue:  "true",
		PathPrefix:   "",
		CustomHeaders: map[string]string{
			"X-Shadow-Request": "true",
		},
	}
}

// IsMutatingMethod returns true for HTTP methods that modify backend state.
func IsMutatingMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// ShouldShadow returns false if the mutation policy mandates dropping mutating requests.
func (c *MutationConfig) ShouldShadow(method string) bool {
	if c.Policy == PolicyDropMutations && IsMutatingMethod(method) {
		return false
	}
	return true
}

// ApplySafetyTransforms modifies the shadow request URL path and headers based on policy.
func (c *MutationConfig) ApplySafetyTransforms(req *http.Request) {
	if req == nil {
		return
	}

	// Always tag shadow requests so candidate backends can identify them
	req.Header.Set("X-Shadow-Request", "true")

	// Apply custom static headers
	for k, v := range c.CustomHeaders {
		req.Header.Set(k, v)
	}

	if !IsMutatingMethod(req.Method) {
		return
	}

	switch c.Policy {
	case PolicyDryRunHeader:
		headerKey := c.DryRunHeader
		if headerKey == "" {
			headerKey = "X-Dry-Run"
		}
		headerVal := c.DryRunValue
		if headerVal == "" {
			headerVal = "true"
		}
		req.Header.Set(headerKey, headerVal)

	case PolicyPathPrefix:
		if c.PathPrefix != "" {
			prefix := strings.TrimSuffix(c.PathPrefix, "/")
			if !strings.HasPrefix(req.URL.Path, prefix) {
				req.URL.Path = prefix + req.URL.Path
			}
		}
	}
}
