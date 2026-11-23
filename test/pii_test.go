package test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/alexandrmotologa/shadowtap/pkg/security"
)

func TestPIISanitize_Headers(t *testing.T) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.secret.token")
	headers.Set("Cookie", "session_id=abcdef123456; user_pref=dark")
	headers.Set("X-Api-Key", "sk_live_1234567890abcdef")
	headers.Set("Content-Type", "application/json")

	cfg := security.DefaultPIIConfig()
	sanitized := security.SanitizeHeaders(headers, cfg)

	if strings.Contains(sanitized.Get("Authorization"), "eyJhbGci") {
		t.Fatalf("Authorization header was not masked: %s", sanitized.Get("Authorization"))
	}
	if strings.Contains(sanitized.Get("Cookie"), "session_id") {
		t.Fatalf("Cookie header was not masked: %s", sanitized.Get("Cookie"))
	}
	if strings.Contains(sanitized.Get("X-Api-Key"), "sk_live") {
		t.Fatalf("X-Api-Key was not masked: %s", sanitized.Get("X-Api-Key"))
	}
	if sanitized.Get("Content-Type") != "application/json" {
		t.Fatalf("innocuous header was modified: %s", sanitized.Get("Content-Type"))
	}
}

func TestPIISanitize_JSONBody(t *testing.T) {
	rawJSON := `{
		"username": "johndoe",
		"email": "john.doe@example.com",
		"password": "SuperSecretPassword123!",
		"card_number": "4111 2222 3333 4444",
		"amount": 99.50
	}`

	cfg := security.DefaultPIIConfig()
	sanitized := string(security.SanitizeBody([]byte(rawJSON), cfg))

	if strings.Contains(sanitized, "SuperSecretPassword123!") {
		t.Fatal("password field was not masked")
	}
	if strings.Contains(sanitized, "john.doe@example.com") {
		t.Fatal("email address was not masked")
	}
	if strings.Contains(sanitized, "4111 2222 3333 4444") {
		t.Fatal("credit card was not masked")
	}
	if !strings.Contains(sanitized, "johndoe") {
		t.Fatal("non-sensitive field username was erroneously removed")
	}
	if !strings.Contains(sanitized, "99.5") {
		t.Fatal("amount field was erroneously modified")
	}
}

func TestMutationGuard_DryRunHeader(t *testing.T) {
	req, _ := http.NewRequest("POST", "http://candidate-service/api/v1/orders", nil)
	cfg := security.DefaultMutationConfig()

	cfg.ApplySafetyTransforms(req)

	if req.Header.Get("X-Dry-Run") != "true" {
		t.Fatalf("expected X-Dry-Run header to be 'true', got %s", req.Header.Get("X-Dry-Run"))
	}
	if req.Header.Get("X-Shadow-Request") != "true" {
		t.Fatalf("expected X-Shadow-Request header to be 'true', got %s", req.Header.Get("X-Shadow-Request"))
	}
}

func TestMutationGuard_DropMutations(t *testing.T) {
	cfg := security.MutationConfig{
		Policy: security.PolicyDropMutations,
	}

	if cfg.ShouldShadow("POST") {
		t.Fatal("expected POST to be dropped under PolicyDropMutations")
	}
	if cfg.ShouldShadow("DELETE") {
		t.Fatal("expected DELETE to be dropped under PolicyDropMutations")
	}
	if !cfg.ShouldShadow("GET") {
		t.Fatal("expected GET to be allowed under PolicyDropMutations")
	}
}
