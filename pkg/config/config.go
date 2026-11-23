package config

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config encapsulates declarative runtime options for shadowtap.
type Config struct {
	Version       string                 `yaml:"version" json:"version"`
	Server        ServerConfig           `yaml:"server" json:"server"`
	Cockpit       CockpitConfig          `yaml:"cockpit" json:"cockpit"`
	Sampling      SamplingConfig         `yaml:"sampling" json:"sampling"`
	Exclusions    []string               `yaml:"exclusions" json:"exclusions"`
	ShadowHeaders map[string]string      `yaml:"shadow_headers" json:"shadow_headers"`
	Security      SecurityConfig         `yaml:"security" json:"security"`
	Diff          DiffConfig             `yaml:"diff" json:"diff"`
	Alerting      AlertingConfig         `yaml:"alerting" json:"alerting"`
	Recorder      RecorderConfig         `yaml:"recorder" json:"recorder"`
}

type ServerConfig struct {
	Listen       string `yaml:"listen" json:"listen"`
	PrimaryURL   string `yaml:"primary_url" json:"primary_url"`
	ShadowURL    string `yaml:"shadow_url" json:"shadow_url"`
	MaxBodyBytes int64  `yaml:"max_body_bytes" json:"max_body_bytes"`
	Workers      int    `yaml:"workers" json:"workers"`
	QueueCap     int    `yaml:"queue_cap" json:"queue_cap"`
}

type CockpitConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	Port    int  `yaml:"port" json:"port"`
}

type RouteSamplingRule struct {
	PathPattern string  `yaml:"path_pattern" json:"path_pattern"`
	Rate        float64 `yaml:"rate" json:"rate"`
}

type HeaderFilterRule struct {
	Header string `yaml:"header" json:"header"`
	Value  string `yaml:"value" json:"value"`
}

type SamplingConfig struct {
	DefaultRate   float64             `yaml:"default_rate" json:"default_rate"`
	Routes        []RouteSamplingRule `yaml:"routes" json:"routes"`
	HeaderFilters []HeaderFilterRule  `yaml:"header_filters" json:"header_filters"`
}

type SecurityConfig struct {
	MaskPII        bool   `yaml:"mask_pii" json:"mask_pii"`
	DryRunHeader   string `yaml:"dry_run_header" json:"dry_run_header"`
	DryRunValue    string `yaml:"dry_run_value" json:"dry_run_value"`
	MutationPolicy string `yaml:"mutation_policy" json:"mutation_policy"`
	PathPrefix     string `yaml:"path_prefix" json:"path_prefix"`
}

type DiffConfig struct {
	IgnoreArrayOrder    bool     `yaml:"ignore_array_order" json:"ignore_array_order"`
	NumericEpsilon      float64  `yaml:"numeric_epsilon" json:"numeric_epsilon"`
	IgnoredPaths        []string `yaml:"ignored_paths" json:"ignored_paths"`
	RegressionThreshold float64  `yaml:"regression_threshold" json:"regression_threshold"`
}

type AlertingConfig struct {
	WebhookURL         string  `yaml:"webhook_url" json:"webhook_url"`
	SRRThreshold       float64 `yaml:"srr_threshold" json:"srr_threshold"` // 0.05 = 5%
	AlertOnStatusDrift bool    `yaml:"alert_on_status_drift" json:"alert_on_status_drift"`
}

type RecorderConfig struct {
	Enabled     bool   `yaml:"enabled" json:"enabled"`
	OutputFile  string `yaml:"output_file" json:"output_file"`
	BufferLimit int    `yaml:"buffer_limit" json:"buffer_limit"`
}

// DefaultConfig provides production-ready defaults.
func DefaultConfig() Config {
	return Config{
		Version: "1",
		Server: ServerConfig{
			Listen:       ":8080",
			MaxBodyBytes: 10 * 1024 * 1024,
			Workers:      16,
			QueueCap:     5000,
		},
		Cockpit: CockpitConfig{
			Enabled: true,
			Port:    8989,
		},
		Sampling: SamplingConfig{
			DefaultRate: 1.0,
			Routes:      nil,
		},
		Exclusions: []string{
			"/healthz",
			"/health",
			"/metrics",
			"/ready",
		},
		ShadowHeaders: make(map[string]string),
		Security: SecurityConfig{
			MaskPII:        true,
			DryRunHeader:   "X-Dry-Run",
			DryRunValue:    "true",
			MutationPolicy: "DRY_RUN_HEADER",
		},
		Diff: DiffConfig{
			IgnoreArrayOrder:    true,
			NumericEpsilon:      1e-5,
			RegressionThreshold: 0.0,
			IgnoredPaths: []string{
				"*.timestamp",
				"*_timestamp",
				"*.created_at",
				"*.updated_at",
				"*.requestId",
				"*.traceId",
			},
		},
		Alerting: AlertingConfig{
			SRRThreshold:       0.02,
			AlertOnStatusDrift: true,
		},
		Recorder: RecorderConfig{
			Enabled:     false,
			OutputFile:  "./data/recorded_traffic.ndjson",
			BufferLimit: 5000,
		},
	}
}

// LoadFromFile reads and parses a YAML or JSON configuration file.
func LoadFromFile(filePath string) (*Config, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", filePath, err)
	}

	cfg := DefaultConfig()
	ext := strings.ToLower(filepath.Ext(filePath))

	if ext == ".json" {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse json config: %w", err)
		}
	} else {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse yaml config: %w", err)
		}
	}

	return &cfg, nil
}

// IsExcluded checks if an HTTP request path is marked for exclusion (e.g. /healthz).
func (c *Config) IsExcluded(reqPath string) bool {
	pLower := strings.ToLower(reqPath)
	for _, exc := range c.Exclusions {
		excLower := strings.ToLower(exc)
		if pLower == excLower {
			return true
		}
		if matched, err := path.Match(excLower, pLower); err == nil && matched {
			return true
		}
		if strings.HasSuffix(excLower, "*") {
			prefix := strings.TrimSuffix(excLower, "*")
			if strings.HasPrefix(pLower, prefix) {
				return true
			}
		}
	}
	return false
}

// GetSamplingRate calculates the effective sampling rate for a given endpoint path.
func (c *Config) GetSamplingRate(reqPath string) float64 {
	pLower := strings.ToLower(reqPath)
	for _, rule := range c.Sampling.Routes {
		patLower := strings.ToLower(rule.PathPattern)
		if pLower == patLower {
			return rule.Rate
		}
		if matched, err := path.Match(patLower, pLower); err == nil && matched {
			return rule.Rate
		}
		// Prefix match if wildcard at end
		if strings.HasSuffix(patLower, "*") {
			prefix := strings.TrimSuffix(patLower, "*")
			if strings.HasPrefix(pLower, prefix) {
				return rule.Rate
			}
		}
	}
	return c.Sampling.DefaultRate
}

// MatchesHeaderFilters validates that inbound headers satisfy configured header filters.
// If no header filters are defined, it returns true by default.
func (c *Config) MatchesHeaderFilters(headers http.Header) bool {
	if len(c.Sampling.HeaderFilters) == 0 {
		return true
	}

	for _, filter := range c.Sampling.HeaderFilters {
		val := headers.Get(filter.Header)
		if val == "" || (filter.Value != "" && !strings.EqualFold(val, filter.Value)) {
			return false
		}
	}
	return true
}

// ApplyShadowHeaders injects configured overrides into outbound shadow requests.
func (c *Config) ApplyShadowHeaders(req *http.Request) {
	if req == nil {
		return
	}
	for k, v := range c.ShadowHeaders {
		req.Header.Set(k, v)
	}
}
