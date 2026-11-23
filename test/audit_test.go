package test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexandrmotologa/shadowtap/pkg/audit"
)

func TestAudit_ReplayTrafficAndJUnit(t *testing.T) {
	// 1. Mock Staging Candidate Server
	stagingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/health":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status": "UP"}`))
		case "/api/v1/user":
			w.WriteHeader(http.StatusOK)
			// Return exact match
			_, _ = w.Write([]byte(`{"id": 42, "role": "editor"}`))
		case "/api/v1/broken":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error": "failed"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer stagingServer.Close()

	// 2. Generate a temporary NDJSON traffic log
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "traffic.ndjson")
	junitPath := filepath.Join(tmpDir, "junit.xml")

	entries := []audit.RecordedEntry{
		{
			ID:             "rec-1",
			Method:         "GET",
			Path:           "/api/v1/health",
			ExpectedStatus: 200,
			ExpectedBody:   `{"status": "UP"}`,
		},
		{
			ID:             "rec-2",
			Method:         "GET",
			Path:           "/api/v1/user",
			ExpectedStatus: 200,
			ExpectedBody:   `{"id": 42, "role": "editor"}`,
		},
		{
			ID:             "rec-3",
			Method:         "GET",
			Path:           "/api/v1/broken",
			ExpectedStatus: 200, // primary returned 200, candidate returned 500
			ExpectedBody:   `{"ok": true}`,
		},
	}

	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("failed to create temp traffic log: %v", err)
	}
	for _, e := range entries {
		data, _ := json.Marshal(e)
		_, _ = f.Write(append(data, '\n'))
	}
	_ = f.Close()

	// 3. Run audit with max-srr 0.50 (1 failure out of 3 = 33.3% SRR, which passes 50% limit)
	summary, err := audit.Run(audit.AuditConfig{
		TrafficLogPath: logPath,
		TargetURL:      stagingServer.URL,
		MaxSRR:         0.50,
		JUnitReport:    junitPath,
		Timeout:        2 * time.Second,
	})
	if err != nil {
		t.Fatalf("audit failed unexpectedly: %v", err)
	}

	if summary.TotalRequests != 3 {
		t.Fatalf("expected 3 requests, got %d", summary.TotalRequests)
	}
	if summary.Regressed != 1 {
		t.Fatalf("expected 1 regression, got %d", summary.Regressed)
	}
	if summary.Matched != 2 {
		t.Fatalf("expected 2 matches, got %d", summary.Matched)
	}
	if !summary.Passed {
		t.Fatal("expected audit run to pass within 50% tolerance limit")
	}

	// Verify JUnit report was generated on disk
	junitData, err := os.ReadFile(junitPath)
	if err != nil {
		t.Fatalf("failed to read junit report: %v", err)
	}
	if len(junitData) == 0 {
		t.Fatal("junit report is empty")
	}
}
