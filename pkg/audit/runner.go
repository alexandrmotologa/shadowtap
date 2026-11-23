package audit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/alexandrmotologa/shadowtap/pkg/diff"
	"github.com/alexandrmotologa/shadowtap/pkg/security"
	"github.com/alexandrmotologa/shadowtap/pkg/storage"
)

// RecordedEntry models an exported production transaction in NDJSON format.
type RecordedEntry struct {
	ID             string            `json:"id"`
	Timestamp      string            `json:"timestamp"`
	Method         string            `json:"method"`
	Path           string            `json:"path"`
	Query          string            `json:"query"`
	RequestHeaders map[string]string `json:"headers"`
	RequestBody    string            `json:"body"`
	ExpectedStatus int               `json:"expected_status"`
	ExpectedBody   string            `json:"expected_body"`
}

// AuditConfig parameters for offline verification.
type AuditConfig struct {
	TrafficLogPath string
	TargetURL      string
	MaxSRR         float64
	JUnitReport    string
	Timeout        time.Duration
}

// AuditSummary contains aggregate results from an audit run.
type AuditSummary struct {
	TotalRequests int
	Matched       int
	Regressed     int
	SRR           float64
	Passed        bool
}

// Run executes the regression audit across logged transactions.
func Run(cfg AuditConfig) (*AuditSummary, error) {
	file, err := os.Open(cfg.TrafficLogPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open traffic log %q: %w", cfg.TrafficLogPath, err)
	}
	defer file.Close()

	targetURL, err := url.Parse(cfg.TargetURL)
	if err != nil {
		return nil, fmt.Errorf("invalid target url %q: %w", cfg.TargetURL, err)
	}

	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}

	client := &http.Client{
		Timeout: cfg.Timeout,
	}

	scanner := bufio.NewScanner(file)
	pairs := make([]diff.ShadowPair, 0)
	maskCfg := diff.DefaultMaskConfig()
	piiCfg := security.DefaultPIIConfig()
	mutCfg := security.DefaultMutationConfig()

	lineNum := 0
	total := 0
	regressed := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		var entry RecordedEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("line %d: invalid recorded entry JSON: %w", lineNum, err)
		}

		total++
		txID := entry.ID
		if txID == "" {
			txID = fmt.Sprintf("audit-%d", total)
		}

		// Replay against target
		destURL := *targetURL
		destURL.Path = entry.Path
		destURL.RawQuery = entry.Query

		reqBody := security.SanitizeBody([]byte(entry.RequestBody), piiCfg)
		req, err := http.NewRequestWithContext(context.Background(), entry.Method, destURL.String(), bytes.NewReader(reqBody))
		if err != nil {
			return nil, fmt.Errorf("line %d: failed to create request: %w", lineNum, err)
		}

		for k, v := range entry.RequestHeaders {
			req.Header.Set(k, v)
		}
		mutCfg.ApplySafetyTransforms(req)

		start := time.Now()
		resp, respErr := client.Do(req)
		duration := time.Since(start)

		var targetSnapshot diff.ResponseSnapshot
		if respErr != nil {
			targetSnapshot = diff.NewResponseSnapshot(0, http.Header{}, "", duration, respErr)
		} else {
			respBytes, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			targetSnapshot = diff.NewResponseSnapshot(resp.StatusCode, resp.Header, string(respBytes), duration, nil)
		}

		expectedSnapshot := diff.NewResponseSnapshot(entry.ExpectedStatus, http.Header{}, entry.ExpectedBody, 0, nil)

		diffResult := diff.CompareSnapshots(expectedSnapshot, targetSnapshot, &maskCfg, diff.DefaultCompareOptions())
		if !diffResult.IsMatch {
			regressed++
		}

		pair := diff.ShadowPair{
			ID:             txID,
			Timestamp:      time.Now(),
			Method:         entry.Method,
			Path:           entry.Path,
			Query:          entry.Query,
			RequestHeaders: req.Header.Clone(),
			RequestBody:    string(reqBody),
			Primary:        expectedSnapshot,
			Shadow:         targetSnapshot,
			Diff:           diffResult,
		}
		pairs = append(pairs, pair)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading traffic log failed: %w", err)
	}

	srr := 0.0
	if total > 0 {
		srr = float64(regressed) / float64(total)
	}

	passed := srr <= cfg.MaxSRR

	// Output JUnit if designated
	if cfg.JUnitReport != "" {
		if err := storage.WriteJUnitReport(cfg.JUnitReport, pairs, srr, cfg.MaxSRR); err != nil {
			return nil, fmt.Errorf("failed to write junit report: %w", err)
		}
	}

	summary := &AuditSummary{
		TotalRequests: total,
		Matched:       total - regressed,
		Regressed:     regressed,
		SRR:           srr,
		Passed:        passed,
	}

	return summary, nil
}
