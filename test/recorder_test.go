package test

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexandrmotologa/shadowtap/pkg/diff"
	"github.com/alexandrmotologa/shadowtap/pkg/recorder"
)

func TestTrafficRecorder_RecordAndFlush(t *testing.T) {
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "traffic.ndjson")

	rec, err := recorder.NewTrafficRecorder(outPath, 100)
	if err != nil {
		t.Fatalf("failed to create recorder: %v", err)
	}

	headers := http.Header{}
	headers.Set("Content-Type", "application/json")

	pair1 := diff.ShadowPair{
		ID:             "tx-rec-1",
		Timestamp:      time.Now(),
		Method:         "GET",
		Path:           "/api/v1/test",
		RequestHeaders: headers,
		RequestBody:    "",
		Primary: diff.ResponseSnapshot{
			StatusCode: 200,
			Body:       `{"status":"OK"}`,
			LatencyMs:  12.5,
		},
	}

	rec.Record(pair1)
	if err := rec.Close(); err != nil {
		t.Fatalf("failed to close recorder: %v", err)
	}

	file, err := os.Open(outPath)
	if err != nil {
		t.Fatalf("failed to read recorded file: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lines := 0
	for scanner.Scan() {
		lines++
	}

	if lines != 1 {
		t.Fatalf("expected 1 recorded line in NDJSON, got %d", lines)
	}
}
