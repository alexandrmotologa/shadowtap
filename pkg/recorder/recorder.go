package recorder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/alexandrmotologa/shadowtap/pkg/audit"
	"github.com/alexandrmotologa/shadowtap/pkg/diff"
)

// TrafficRecorder streams observed production transactions to an NDJSON file for offline audits.
type TrafficRecorder struct {
	mu         sync.Mutex
	filePath   string
	file       *os.File
	queue      chan audit.RecordedEntry
	stopCh     chan struct{}
	wg         sync.WaitGroup
	isRecording bool
}

// NewTrafficRecorder initializes a non-blocking traffic recorder.
func NewTrafficRecorder(outputPath string, bufferLimit int) (*TrafficRecorder, error) {
	if outputPath == "" {
		outputPath = "./data/recorded_traffic.ndjson"
	}
	if bufferLimit <= 0 {
		bufferLimit = 5000
	}

	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory %q: %w", dir, err)
	}

	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open output file %q: %w", outputPath, err)
	}

	tr := &TrafficRecorder{
		filePath:    outputPath,
		file:        file,
		queue:       make(chan audit.RecordedEntry, bufferLimit),
		stopCh:      make(chan struct{}),
		isRecording: true,
	}

	tr.wg.Add(1)
	go tr.writeLoop()

	return tr, nil
}

// Record queues a transaction pair for persistent storage.
func (tr *TrafficRecorder) Record(pair diff.ShadowPair) {
	if tr == nil || !tr.isRecording {
		return
	}

	headersMap := make(map[string]string)
	for k, v := range pair.RequestHeaders {
		if len(v) > 0 {
			headersMap[k] = v[0]
		}
	}

	entry := audit.RecordedEntry{
		ID:             pair.ID,
		Timestamp:      pair.Timestamp.Format(time.RFC3339),
		Method:         pair.Method,
		Path:           pair.Path,
		Query:          pair.Query,
		RequestHeaders: headersMap,
		RequestBody:    pair.RequestBody,
		ExpectedStatus: pair.Primary.StatusCode,
		ExpectedBody:   pair.Primary.Body,
	}

	select {
	case tr.queue <- entry:
	default:
		// Drop if file writer is saturated to avoid blocking
	}
}

// writeLoop processes queued entries and writes line-delimited JSON.
func (tr *TrafficRecorder) writeLoop() {
	defer tr.wg.Done()

	for {
		select {
		case <-tr.stopCh:
			// Drain remaining
			for len(tr.queue) > 0 {
				entry := <-tr.queue
				tr.writeEntry(entry)
			}
			return
		case entry := <-tr.queue:
			tr.writeEntry(entry)
		}
	}
}

func (tr *TrafficRecorder) writeEntry(entry audit.RecordedEntry) {
	data, err := json.Marshal(entry)
	if err == nil {
		tr.mu.Lock()
		_, _ = tr.file.Write(append(data, '\n'))
		tr.mu.Unlock()
	}
}

// Close gracefully stops recording and flushes pending writes to disk.
func (tr *TrafficRecorder) Close() error {
	if tr == nil || !tr.isRecording {
		return nil
	}
	tr.isRecording = false
	close(tr.stopCh)
	tr.wg.Wait()

	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.file != nil {
		return tr.file.Close()
	}
	return nil
}
