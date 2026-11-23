# shadowtap Benchmark and Performance Validation

## 1. Test Methodology

Performance measurements were executed using Go's built-in testing harness (`go test -bench`) across concurrent reverse proxy forwarding and isolated semantic comparison routines.

Environment Specifications:
- Architecture: `x86_64` (Intel Core i5-1135G7 @ 2.40GHz)
- Operating System: Windows 11 (64-bit)
- Go Version: `go1.27.0`
- Concurrency Level: 8 to 16 parallel worker goroutines

---

## 2. Benchmark Results

### 2.1 Proxy Forwarding Overhead (`BenchmarkProxyOverhead`)
Measures the complete request handling lifecycle: inbound request interception, memory buffer allocation from `sync.Pool`, body duplication, primary dispatch, response capture, and async queue handoff.

```
BenchmarkProxyOverhead-8    913 ops    1.32 ms/op    50,635 B/op    197 allocs/op
```

Key Takeaway:
- The measured proxy forwarding overhead is **1.32 ms/op**, strictly satisfying the `< 1.5ms` latency overhead target.
- Allocations remain tightly bounded due to buffer pooling, preventing GC pauses during high request volumes.

### 2.2 Semantic Diff Engine Throughput (`BenchmarkSemanticDiffing`)
Measures tree traversal, volatile field filtering (regex parsing of ISO-8601 timestamps and UUIDs), Jaccard distance calculation, and mismatch classification across structured payloads.

```
BenchmarkSemanticDiffing-8    19,972 ops    0.077 ms/op (77 µs)    5,991 B/op    172 allocs/op
```

Key Takeaway:
- A single core evaluates over **12,800 full semantic payload diffs per second**.
- Latency per diff is **77 microseconds**, ensuring asynchronous shadow workers keep pace with live traffic streams.

---

## 3. Reproduction Instructions

To reproduce these benchmark metrics locally:

```bash
# Clone and navigate to shadowtap repository
cd shadowtap

# Execute benchmark suite with allocation metrics
go test -v -bench="." -benchtime=2s github.com/alexandrmotologa/shadowtap/test
```
