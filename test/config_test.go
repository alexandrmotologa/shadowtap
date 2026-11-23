package test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexandrmotologa/shadowtap/pkg/config"
)

func TestConfig_YAMLAndRouteMatching(t *testing.T) {
	yamlContent := `
server:
  listen: ":9090"
  primary_url: "http://primary:8080"
  shadow_url: "http://shadow:8080"

sampling:
  default_rate: 0.25
  routes:
    - path_pattern: "/api/v1/checkout*"
      rate: 1.0
    - path_pattern: "/api/v1/search*"
      rate: 0.05
  header_filters:
    - header: "X-Beta-Tester"
      value: "true"

exclusions:
  - "/healthz"
  - "/metrics*"

shadow_headers:
  "X-Canary": "v2"
`
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "shadowtap.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := config.LoadFromFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// 1. Verify basic server config
	if cfg.Server.Listen != ":9090" {
		t.Errorf("expected listen :9090, got %s", cfg.Server.Listen)
	}

	// 2. Verify sampling rates
	if rate := cfg.GetSamplingRate("/api/v1/checkout/pay"); rate != 1.0 {
		t.Errorf("expected rate 1.0 for checkout, got %f", rate)
	}
	if rate := cfg.GetSamplingRate("/api/v1/search?q=shoes"); rate != 0.05 {
		t.Errorf("expected rate 0.05 for search, got %f", rate)
	}
	if rate := cfg.GetSamplingRate("/api/v1/users"); rate != 0.25 {
		t.Errorf("expected default rate 0.25 for users, got %f", rate)
	}

	// 3. Verify exclusions
	if !cfg.IsExcluded("/healthz") {
		t.Errorf("expected /healthz to be excluded")
	}
	if !cfg.IsExcluded("/metrics/prometheus") {
		t.Errorf("expected /metrics/prometheus to be excluded via pattern")
	}
	if cfg.IsExcluded("/api/v1/orders") {
		t.Errorf("expected /api/v1/orders to not be excluded")
	}

	// 4. Verify header filters
	hMatch := http.Header{}
	hMatch.Set("X-Beta-Tester", "true")
	if !cfg.MatchesHeaderFilters(hMatch) {
		t.Errorf("expected header filter to match")
	}

	hNoMatch := http.Header{}
	hNoMatch.Set("X-Beta-Tester", "false")
	if cfg.MatchesHeaderFilters(hNoMatch) {
		t.Errorf("expected header filter to reject")
	}

	// 5. Verify shadow headers injection
	req, _ := http.NewRequest("GET", "http://shadow:8080/test", nil)
	cfg.ApplyShadowHeaders(req)
	if req.Header.Get("X-Canary") != "v2" {
		t.Errorf("expected shadow header X-Canary: v2, got %s", req.Header.Get("X-Canary"))
	}
}
