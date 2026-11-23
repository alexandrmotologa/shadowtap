package proxy

import (
	"bytes"
	"io"
	"net/http"
	"sync"
)

var (
	// bufferPool reuses byte buffers across high-throughput request cycles.
	bufferPool = sync.Pool{
		New: func() any {
			return new(bytes.Buffer)
		},
	}
)

// ClonedRequest carries duplicate data for asynchronous shadowing.
type ClonedRequest struct {
	ID        string
	Method    string
	URLPath   string
	Query     string
	Headers   http.Header
	Body      []byte
	Host      string
}

// CloneInboundRequest safely duplicates the request body and metadata for async processing.
// The original request's Body is reconstituted so that Primary forwarding is unaffected.
func CloneInboundRequest(req *http.Request, maxBodyBytes int64) (*ClonedRequest, error) {
	if maxBodyBytes <= 0 {
		maxBodyBytes = 10 * 1024 * 1024 // 10MB default
	}

	cloned := &ClonedRequest{
		Method:  req.Method,
		URLPath: req.URL.Path,
		Query:   req.URL.RawQuery,
		Headers: req.Header.Clone(),
		Host:    req.Host,
	}

	if req.Body == nil || req.Body == http.NoBody {
		return cloned, nil
	}

	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufferPool.Put(buf)

	// Read bounded bytes
	limitedReader := io.LimitReader(req.Body, maxBodyBytes)
	_, err := buf.ReadFrom(limitedReader)
	_ = req.Body.Close()

	if err != nil && err != io.EOF {
		return nil, err
	}

	bodyBytes := buf.Bytes()
	// Allocate independent slice for the cloned request
	clonedBody := make([]byte, len(bodyBytes))
	copy(clonedBody, bodyBytes)
	cloned.Body = clonedBody

	// Reconstruct the original request's Body with an in-memory reader for Primary
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	return cloned, nil
}
