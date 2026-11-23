# shadowtap: Architecture and Engineering Master Plan
Production Traffic Shadowing, Semantic Diffing & Zero-Risk Dark Launching Proxy
Target: Ultra-Low-Latency Go Proxy (< 20MB RAM, < 1.5ms overhead) with Embedded Diff Cockpit

---

## 1. Problem Statement and Scope

### 1.1 The Risk of Critical Microservice Migrations
When enterprise engineering teams rewrite, upgrade, or refactor mission-critical microservices (e.g., rewriting Python/Node.js services in Go, upgrading GraphQL/gRPC APIs, or refactoring pricing engines), traditional testing environments fall short:
1. **Synthetic Staging Inadequacy**: Staging environments lack production traffic diversity, payload anomalies, realistic network latencies, and edge-case parameter combinations.
2. **High-Stakes Canary Deployments**: Even a 1% canary release exposes real human users to subtle bugs, broken schemas, or transactional errors that degrade customer trust.
3. **Volatile Noise in Response Diffing**: Naively comparing HTTP responses between two service versions generates thousands of false alarms caused by non-deterministic fields (such as timestamps, UUIDs, server IDs, and JWT signatures).
4. **Mutating Request Hazards**: Shadowing requests containing write operations (`POST`, `PUT`, `DELETE`) risks triggering duplicate database insertions, double payments, or invalid state mutations.

### 1.2 The shadowtap Solution
`shadowtap` is a high-speed traffic shadowing reverse proxy and dark-launching sentinel written in pure Go:
- **Asynchronous Zero-Impact Duplication**: Intercepts inbound HTTP/1.1, HTTP/2, and gRPC requests. Forwards the request immediately to the primary service, while asynchronously cloning the payload and dispatching it to the shadow/candidate service without delaying client responses.
- **Automated PII Masking & Mutation Neutralization**: Automatically sanitizes sensitive PII fields (credit cards, passwords, phone numbers) and injects safety headers (`X-Dry-Run: true`, `X-Shadowed: true`) on mutating endpoints.
- **Intelligent Semantic JSON Diffing**: Ignores configurable volatile fields (RFC 3339 timestamps, UUIDs, monotonic IDs, trace headers) while strictly comparing schema structure, types, and substantive values.
- **Statistical Regression Scoring**: Computes the **Semantic Regression Rate (SRR)** and latency distribution deltas ($\Delta p_{50}$, $\Delta p_{99}$) across arbitrary traffic windows.
- **Embedded Web Cockpit**: A visual studio on port 8989 with a side-by-side JSON diff viewer, real-time regression alerts, and route-by-route health scorecards.

---

## 2. System Architecture

```
+-------------------------------------------------------------+
|                      Inbound Traffic                        |
|        (API Gateway / Ingress / External Clients)           |
+-------------------------------------------------------------+
                               |
                               v
+-------------------------------------------------------------+
|                      shadowtap Proxy                        |
|                                                             |
|  +---------------------+        +------------------------+  |
|  | Request Cloner &    |------->| Primary Dispatcher     |  |
|  | Header Sanitizer    |        | (Synchronous, Low Lat) |  |
|  +---------------------+        +------------------------+  |
|            |                                 |              |
|            v                                 v              |
|  +---------------------+        +------------------------+  |
|  | PII Masker &        |        | Primary Service        |  |
|  | Mutation Guard      |        | (Live Production)      |  |
|  +---------------------+        +------------------------+  |
|            |                                 |              |
|            v                                 | Response R_1 |
|  +---------------------+                     v              |
|  | Async Shadow Pool   |--------> Client Receives R_1       |
|  | (Configurable Ratio)|          (Zero Added Delay)        |
|  +---------------------+                                    |
|            |                                                |
|            v                                                |
|  +---------------------+                                    |
|  | Shadow Service      |                                    |
|  | (Candidate Release) |                                    |
|  +---------------------+                                    |
|            |                                                |
|            | Response R_2                                   |
|            v                                                |
|  +-------------------------------------------------------+  |
|  |           Semantic Response Diffing Engine            |  |
|  |  - Volatile Field Masking (UUID, Timestamp, Hash)     |  |
|  |  - Structural Schema & Type Invariance Check          |  |
|  |  - Latency Delta Measurement: (t_shadow - t_primary)  |  |
|  +-------------------------------------------------------+  |
+------------------------------|------------------------------+
                               |
                               v
+-------------------------------------------------------------+
|                   Observability & Cockpit                   |
|  1. Semantic Regression Rate (SRR) Scorecard                |
|  2. Real-Time Prometheus Metrics (/metrics)                 |
|  3. Embedded Web Cockpit (:8989) Side-by-Side Diff Viewer   |
+-------------------------------------------------------------+
```

---

## 3. Mathematical & Algorithmic Formulation

### 3.1 Non-Blocking Asynchronous Shadowing
Let $t_0$ denote the arrival timestamp of request $Q$. The primary response is dispatched at $t_1 = t_0 + \tau_{\text{primary}}$. The client response latency $\tau_{\text{client}}$ satisfies:

$$\tau_{\text{client}} = \tau_{\text{primary}} + \epsilon_{\text{proxy}} \quad \text{where } \epsilon_{\text{proxy}} < 1.5\text{ms}$$

The shadow request $Q'$ is enqueued into a lock-free ring buffer of capacity $K$. If the ring buffer fills during a traffic spike, shadow requests are shed to guarantee zero performance degradation on primary traffic.

### 3.2 Semantic JSON Tree Flattening
Given a JSON payload $J$, it is decomposed into a flattened set of key-path value tuples $\mathcal{T}(J)$:

$$\mathcal{T}(J) = \left\{ (p_k, \text{Type}(v_k), v_k) : p_k \in \text{Paths}(J) \right\}$$

A volatile masking filter $\mathcal{M}$ removes elements where $p_k$ matches configured noise patterns or whose values match dynamic regexes (e.g., ISO-8601, UUIDv4):

$$\mathcal{T}_{\text{clean}}(J) = \mathcal{T}(J) \setminus \left\{ (p, t, v) : p \in \mathcal{M}_{\text{keys}} \lor \text{MatchesRegex}(v, \mathcal{M}_{\text{regex}}) \right\}$$

### 3.3 Semantic Distance ($D_{\text{sem}}$) and Jaccard Dissimilarity
The semantic difference between primary response $R_{\text{prim}}$ and shadow response $R_{\text{shad}}$ is computed over the cleaned tuple sets:

$$D_{\text{sem}}(R_{\text{prim}}, R_{\text{shad}}) = \frac{|\mathcal{T}_{\text{clean}}(R_{\text{prim}}) \mathbin{\Delta} \mathcal{T}_{\text{clean}}(R_{\text{shad}})|}{|\mathcal{T}_{\text{clean}}(R_{\text{prim}}) \cup \mathcal{T}_{\text{clean}}(R_{\text{shad}})|}$$

Where $\mathbin{\Delta}$ denotes the symmetric set difference.
- If $D_{\text{sem}} = 0.0$, the responses are semantically identical.
- If $D_{\text{sem}} > 0.0$, an actionable semantic drift has occurred.

### 3.4 Semantic Regression Rate (SRR)
Across an observation window of $N$ shadowed requests:

$$\text{SRR} = \frac{1}{N} \sum_{i=1}^N \mathbf{1}\left(D_{\text{sem}}(R_{\text{prim}}^{(i)}, R_{\text{shad}}^{(i)}) > \theta_{\text{tolerance}}\right)$$

An $\text{SRR} > 0.01$ (1% regression rate) indicates backward incompatibility in the candidate service.

---

## 4. Component Design and Subsystems

### 4.1 Reverse Proxy & Cloner (`pkg/proxy`)
- Built on Go's optimized `net/http/httputil.ReverseProxy`.
- Zero-copy body tee-reader streams payloads to primary while duplicating bytes into an in-memory buffer for shadowing.
- Configurable shadow sampling rate (`--shadow-sample-rate 0.25` for 25% traffic sampling).

### 4.2 Security & Mutation Guard (`pkg/security`)
- PII Sanitizer: Replaces credit cards, SSNs, and bearer tokens with deterministic hashes (`[MASKED-SHA256]`).
- Mutation Guard: Appends configurable headers (`X-Dry-Run: true`, `X-Shadow-Request: true`) to all `POST`, `PUT`, `PATCH`, and `DELETE` requests so target backends can skip database commits.

### 4.3 Semantic Diff Engine (`pkg/diff`)
- Fast recursive JSON parser that builds the flattened path tuple map.
- Type mismatch detector: Flags when a number is returned as a string (e.g., `42` vs `"42"`).
- Array order tolerance: Configurable flag to treat JSON arrays as sets when order is non-deterministic.

### 4.4 Embedded Diff Cockpit (`pkg/cockpit`)
- Serves interactive dashboard on port 8989.
- Features:
  - **Side-by-Side Unified Diff**: Color-coded red/green diff view of mismatched request/response pairs.
  - **Route Regression Scorecard**: Real-time table ranking routes by error rate and latency delta.
  - **Latency Distribution Waterfall**: Compares p50, p95, and p99 response times between production and candidate.

---

## 5. Command-Line Interface (CLI)

```bash
# Start shadowtap proxying production traffic to a candidate release
shadowtap start \
  --listen ":8080" \
  --primary "http://prod-service:8080" \
  --shadow "http://candidate-service:8080" \
  --sample-rate 1.0 \
  --mask-pii \
  --cockpit

# Run an automated regression audit on recorded traffic
shadowtap audit \
  --traffic-log ./data/prod_traffic.ndjson \
  --target "http://staging-service:8080" \
  --max-srr 0.005 \
  --junit ./reports/regression_junit.xml

# Start in dry-run mode for mutating endpoints
shadowtap start \
  --listen ":8080" \
  --primary "http://prod:8080" \
  --shadow "http://canary:8080" \
  --dry-run-header "X-Dry-Run: 1"

# Launch standalone Cockpit UI on diff database
shadowtap cockpit --db ./data/shadow_diffs.db --port 8989
```

---

## 6. Directory Structure

```
B:\workgit\shadowtap\
├── cmd/
│   └── shadowtap/
│       └── main.go                  # Main entry point and CLI commands
├── pkg/
│   ├── proxy/
│   │   ├── server.go                # Reverse proxy core
│   │   ├── cloner.go                # Async request cloner and buffer pool
│   │   └── dispatcher.go            # Shadow worker pool with rate-limiting
│   ├── security/
│   │   ├── pii.go                   # PII pattern detector and masker
│   │   └── mutation.go              # Mutation guard & dry-run injector
│   ├── diff/
│   │   ├── parser.go                # JSON tree parser & flattener
│   │   ├── mask.go                  # Volatile field ignore rules
│   │   ├── comparator.go            # Semantic Jaccard distance calculation
│   │   └── types.go                 # DiffResult, Mismatch, and RouteScore
│   ├── metrics/
│   │   ├── srr.go                   # Semantic Regression Rate calculator
│   │   └── latency.go               # Percentile latency aggregator
│   └── cockpit/
│       ├── server.go                # Embedded HTTP server
│       └── web/
│           ├── index.html           # Unified diff and health cockpit UI
│           ├── app.js               # Dual-pane diff renderer & latency charts
│           └── style.css            # Dark tech clean UI styling
├── test/
│   ├── mock_services.go             # Mock primary & shadow HTTP backends
│   └── diff_test.go                 # Unit tests for JSON semantic comparisons
├── go.mod
├── go.sum
├── README.md
└── MASTER_PLAN.md
```

---

## 7. Implementation Roadmap

### Phase 1: High-Speed Reverse Proxy & Request Cloner
- Initialize Go module `github.com/mtlglabs/shadowtap`.
- Implement non-blocking request cloner with bounded memory queues.
- Verify proxy forwarding overhead is strictly $< 1.5\text{ms}$.

### Phase 2: Security Sanitization & Mutation Guard
- Implement PII regex scanner with zero heap allocations.
- Implement mutation safety wrapper with configurable dry-run headers.

### Phase 3: Semantic Diff Engine & Volatile Masking
- Implement flattened JSON path representation.
- Build regex-based volatile field filter (timestamps, UUIDs).
- Implement Jaccard semantic distance and type drift detection.

### Phase 4: Embedded Cockpit Dashboard
- Implement embedded HTTP server on port 8989.
- Build split-view visual diff inspector showing exact discrepancies.
- Render route-level health bars and latency percentiles.

### Phase 5: Verification and Benchmarks
- Benchmark throughput: sustain 10,000 requests/sec with $< 20\text{MB}$ RAM.
- End-to-end integration tests using synthetic drifting services.
