# shadowtap Architecture Specification

## 1. Overview and Invariants

`shadowtap` is an ultra-low-latency reverse proxy and dark-launching sidecar engineered in pure Go. It enables engineering teams to mirror live production HTTP traffic asynchronously to candidate service releases (canary or staging), compare response payloads semantically in real time, and quantify backward compatibility without customer risk.

### Core System Invariants
1. **Primary Traffic Invariance**: Under no circumstances does shadow dispatch delay or fail the primary response. Client latency $\tau_{\text{client}}$ satisfies $\tau_{\text{client}} = \tau_{\text{primary}} + \epsilon_{\text{proxy}}$ with $\epsilon_{\text{proxy}} < 1.5\text{ms}$.
2. **Backpressure Isolation**: If candidate backends slow down or traffic surges, shadow requests are shed via bounded lock-free buffers. Primary requests are never blocked or throttled.
3. **Data Privacy Invariance**: Inbound sensitive tokens, credentials, credit cards, and PII are redacted or hashed before transmission to candidate environments.
4. **State Mutation Invariance**: Mutating HTTP requests (`POST`, `PUT`, `PATCH`, `DELETE`) are tagged with safety dry-run headers (`X-Dry-Run: true`) or redirected to sandbox paths to prevent duplicate side effects in candidate backends.

---

## 2. Component Diagram

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
|  | Async Shadow Queue  |--------> Client Receives R_1       |
|  | (Bounded Ring)      |          (Zero Added Delay)        |
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
|  3. Embedded Web Cockpit (:8989) Live Diff Studio           |
+-------------------------------------------------------------+
```

---

## 3. Subsystem Breakdown

### 3.1 Request Cloner and Memory Pool (`pkg/proxy`)
- Memory allocations during request cloning are stabilized through `sync.Pool` reuse of `bytes.Buffer`.
- Inbound body bytes are duplicated with `io.LimitReader` (default limit 10MB) to protect against memory exhaustion.
- The original request body is reconstructed with `io.NopCloser(bytes.NewReader(bodyBytes))` to guarantee zero disruption to the primary reverse proxy stream.

### 3.2 Security Sanitization and Mutation Guard (`pkg/security`)
- **PII Scrubbing**: Evaluates headers and JSON bodies against known sensitive keys (`password`, `token`, `secret`, `card_number`, `ssn`) and regex signatures (Luhn credit cards, emails, SSNs).
- **Deterministic Masking**: Sensitive fields are replaced with a cryptographic hash prefix (`[MASKED-SHA256:7f83b1...]`), allowing matching logic to confirm deterministic handling across versions without leaking raw secrets.
- **Mutation Guard**: Enforces `X-Dry-Run: true` and `X-Shadow-Request: true` on non-idempotent operations. Target services can use these headers to roll back transactional changes at the database boundary.

### 3.3 Semantic Diff Engine and Noise Filter (`pkg/diff`)
- Recursive tree traversal flattens JSON hierarchies into normalized path tuples:
  `T(J) = { (path, type, value) }`
- Noise suppression strips transient volatile elements:
  - ISO-8601 and RFC 3339 timestamps.
  - UUID v1 through v5 strings.
  - JWT tokens and trace IDs.
- Calculates Jaccard Semantic Distance:
  `D_sem = |T_prim Δ T_shad| / |T_prim ∪ T_shad|`
- Flags explicit regressions:
  - `STATUS_DRIFT`: Status code discrepancy (e.g., Primary 200 vs Candidate 500).
  - `TYPE_DRIFT`: Data type mutation (e.g., integer vs string).
  - `MISSING_KEY`: Mandatory field omitted in candidate response.
  - `UNEXPECTED_KEY`: New uncontracted field returned by candidate.

### 3.4 Bounded Telemetry and Storage (`pkg/storage` & `pkg/metrics`)
- Transaction pairs are preserved in an in-memory ring buffer with fixed capacity (default 1000 to 5000 records).
- Concurrent readers access historical data without locks through index offsets.
- Live diff events are published to connected Cockpit browsers via Server-Sent Events (SSE).

---

## 4. Operational Invariants and Failure Recovery

| Failure Scenario | Proxy Behavior | Shadow Candidate Behavior |
| :--- | :--- | :--- |
| **Shadow Candidate Unreachable** | Primary traffic completes normally | Circuit breaker trips to Open, shedding requests for 15s |
| **Sudden Traffic Spike (10x)** | Primary traffic served with priority | Ring buffer overflows safely; shadow tasks dropped without blocking |
| **Candidate Response Timeout** | Primary response already delivered | Shadow context deadline fires after 8s; error recorded |
| **Malformed JSON in Response** | Primary response delivered to client | Diff engine falls back to raw string mismatch inspection |
