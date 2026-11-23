package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alexandrmotologa/shadowtap/pkg/audit"
	"github.com/alexandrmotologa/shadowtap/pkg/cockpit"
	"github.com/alexandrmotologa/shadowtap/pkg/diff"
	"github.com/alexandrmotologa/shadowtap/pkg/metrics"
	"github.com/alexandrmotologa/shadowtap/pkg/proxy"
	"github.com/alexandrmotologa/shadowtap/pkg/security"
	"github.com/alexandrmotologa/shadowtap/pkg/storage"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	switch command {
	case "start":
		runStart(os.Args[2:])
	case "audit":
		runAudit(os.Args[2:])
	case "cockpit":
		runCockpit(os.Args[2:])
	case "demo":
		runDemo(os.Args[2:])
	case "version":
		fmt.Println("shadowtap v1.0.0 (Go 1.27)")
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`shadowtap - Production Traffic Shadowing, Semantic Diffing & Zero-Risk Dark Launching Proxy

Usage:
  shadowtap <command> [arguments]

Commands:
  start     Start the reverse proxy, traffic cloner, and semantic diff engine
  audit     Replay recorded traffic log against a candidate service and verify SRR
  cockpit   Launch standalone Cockpit web studio
  demo      Run end-to-end live demo with simulated primary, candidate, and traffic
  version   Display shadowtap version information

Run 'shadowtap <command> --help' for details on each command.`)
}

func runStart(args []string) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	listenAddr := fs.String("listen", ":8080", "Proxy listen address")
	primaryTarget := fs.String("primary", "", "Primary upstream production service URL (required)")
	shadowTarget := fs.String("shadow", "", "Shadow candidate target service URL (required)")
	sampleRate := fs.Float64("sample-rate", 1.0, "Traffic sampling rate (0.0 to 1.0)")
	maskPII := fs.Bool("mask-pii", true, "Automatically sanitize sensitive PII in headers and body")
	enableCockpit := fs.Bool("cockpit", true, "Enable embedded Cockpit web studio")
	cockpitPort := fs.Int("cockpit-port", 8989, "Port for Cockpit web studio")
	workers := fs.Int("workers", 16, "Number of concurrent shadow worker goroutines")
	queueCap := fs.Int("queue-cap", 5000, "In-memory ring buffer capacity for shadow queue")
	dryRunHeader := fs.String("dry-run-header", "X-Dry-Run: true", "Dry-run header to inject on mutating requests (Key: Value)")

	_ = fs.Parse(args)

	if *primaryTarget == "" || *shadowTarget == "" {
		fmt.Fprintln(os.Stderr, "Error: Both --primary and --shadow targets are required.")
		fs.Usage()
		os.Exit(1)
	}

	telemetry := metrics.NewGlobalTelemetry()
	store := storage.NewRingBuffer(*queueCap)

	piiCfg := security.DefaultPIIConfig()
	if !*maskPII {
		piiCfg.MaskCreditCards = false
		piiCfg.MaskSSNs = false
		piiCfg.MaskEmails = false
		piiCfg.MaskTokens = false
	}

	mutCfg := security.DefaultMutationConfig()
	if *dryRunHeader != "" {
		parts := strings.SplitN(*dryRunHeader, ":", 2)
		if len(parts) == 2 {
			mutCfg.DryRunHeader = strings.TrimSpace(parts[0])
			mutCfg.DryRunValue = strings.TrimSpace(parts[1])
		}
	}

	dispCfg := proxy.DispatcherConfig{
		ShadowTarget:   *shadowTarget,
		SampleRate:     *sampleRate,
		QueueCapacity:  *queueCap,
		WorkerCount:    *workers,
		RequestTimeout: 8 * time.Second,
		PIIConfig:      piiCfg,
		MutationConfig: mutCfg,
		MaskConfig:     diff.DefaultMaskConfig(),
	}

	dispatcher, err := proxy.NewShadowDispatcher(dispCfg, telemetry, store)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize shadow dispatcher: %v\n", err)
		os.Exit(1)
	}
	defer dispatcher.Stop()

	proxyServer, err := proxy.NewServer(proxy.ProxyServerConfig{
		ListenAddr:    *listenAddr,
		PrimaryTarget: *primaryTarget,
	}, dispatcher)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize reverse proxy: %v\n", err)
		os.Exit(1)
	}

	if *enableCockpit {
		cockpitSrv := cockpit.NewServer(cockpit.ServerConfig{Port: *cockpitPort}, telemetry, store)
		go func() {
			fmt.Printf("[Cockpit] Web studio live at http://localhost:%d\n", *cockpitPort)
			if err := cockpitSrv.Start(); err != nil && err != http.ErrServerClosed {
				fmt.Fprintf(os.Stderr, "Cockpit error: %v\n", err)
			}
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		fmt.Printf("[shadowtap] Proxy listening on %s (Primary: %s, Shadow: %s, SampleRate: %.2f)\n",
			*listenAddr, *primaryTarget, *shadowTarget, *sampleRate)
		if err := proxyServer.Start(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Proxy error: %v\n", err)
			os.Exit(1)
		}
	}()

	<-sigCh
	fmt.Println("\n[shadowtap] Shutting down gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = proxyServer.Shutdown(ctx)
}

func runAudit(args []string) {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	trafficLog := fs.String("traffic-log", "", "Path to NDJSON traffic log file (required)")
	target := fs.String("target", "", "Candidate target service URL (required)")
	maxSRR := fs.Float64("max-srr", 0.01, "Maximum acceptable Semantic Regression Rate (default 0.01 = 1%)")
	junitPath := fs.String("junit", "", "Optional output path for JUnit XML report")

	_ = fs.Parse(args)

	if *trafficLog == "" || *target == "" {
		fmt.Fprintln(os.Stderr, "Error: Both --traffic-log and --target are required.")
		fs.Usage()
		os.Exit(1)
	}

	fmt.Printf("[Audit] Starting regression audit against %s...\n", *target)
	fmt.Printf("[Audit] Replaying traffic from %s (Max SRR: %.2f%%)\n", *trafficLog, *maxSRR*100)

	summary, err := audit.Run(audit.AuditConfig{
		TrafficLogPath: *trafficLog,
		TargetURL:      *target,
		MaxSRR:         *maxSRR,
		JUnitReport:    *junitPath,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Audit execution error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n---------------------------------------------------------")
	fmt.Println("             shadowtap Regression Audit Scorecard         ")
	fmt.Println("---------------------------------------------------------")
	fmt.Printf("Total Transactions Replayed : %d\n", summary.TotalRequests)
	fmt.Printf("Semantically Matched        : %d\n", summary.Matched)
	fmt.Printf("Semantic Regressions        : %d\n", summary.Regressed)
	fmt.Printf("Semantic Regression Rate    : %.3f%% (Threshold: %.3f%%)\n", summary.SRR*100, *maxSRR*100)

	if *junitPath != "" {
		fmt.Printf("JUnit XML Report Generated  : %s\n", *junitPath)
	}
	fmt.Println("---------------------------------------------------------")

	if !summary.Passed {
		fmt.Printf("FAILED: Semantic Regression Rate %.3f%% exceeded tolerance limit %.3f%%\n", summary.SRR*100, *maxSRR*100)
		os.Exit(1)
	}

	fmt.Println("PASSED: Candidate service passed all semantic regression invariants.")
}

func runCockpit(args []string) {
	fs := flag.NewFlagSet("cockpit", flag.ExitOnError)
	port := fs.Int("port", 8989, "Port for Cockpit web studio")
	_ = fs.Parse(args)

	telemetry := metrics.NewGlobalTelemetry()
	store := storage.NewRingBuffer(1000)

	srv := cockpit.NewServer(cockpit.ServerConfig{Port: *port}, telemetry, store)
	fmt.Printf("[Cockpit] Standalone studio listening on http://localhost:%d\n", *port)
	if err := srv.Start(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "Cockpit error: %v\n", err)
		os.Exit(1)
	}
}

// runDemo sets up an instant live environment with Primary, Candidate, and continuous traffic.
func runDemo(args []string) {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	cockpitPort := fs.Int("cockpit-port", 8989, "Port for Cockpit web studio")
	proxyPort := fs.Int("proxy-port", 8080, "Port for shadowtap proxy")
	_ = fs.Parse(args)

	// 1. Mock Primary Service (v1)
	primaryMux := http.NewServeMux()
	primaryMux.HandleFunc("/api/v1/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		time.Sleep(time.Duration(10+rand.Intn(15)) * time.Millisecond)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"user_id": "usr-101", "name": "Alex", "role": "admin", "timestamp": %q}`, time.Now().Format(time.RFC3339))))
	})
	primaryMux.HandleFunc("/api/v1/orders", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		time.Sleep(time.Duration(15+rand.Intn(20)) * time.Millisecond)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"order_id": "ORD-%d", "total": 99.95, "status": "CONFIRMED", "uuid": "4a7b98d2-1c23-4e56-9a01-abcdef012345"}`, 1000+rand.Intn(9000))))
	})
	primaryMux.HandleFunc("/api/v1/checkout", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		time.Sleep(time.Duration(20+rand.Intn(25)) * time.Millisecond)
		_, _ = w.Write([]byte(`{"transaction_id": "tx-881", "success": true, "auth_code": "AUTH-OK"}`))
	})
	primaryServer := httptest.NewServer(primaryMux)
	defer primaryServer.Close()

	// 2. Mock Candidate Service (v2) with intentional realistic drift
	shadowMux := http.NewServeMux()
	shadowMux.HandleFunc("/api/v1/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		time.Sleep(time.Duration(8+rand.Intn(12)) * time.Millisecond) // Faster Go rewrite
		// Intentional Type Drift: user_id returned as integer instead of string in v2
		_, _ = w.Write([]byte(fmt.Sprintf(`{"user_id": 101, "name": "Alex", "role": "admin", "timestamp": %q}`, time.Now().Add(time.Second).Format(time.RFC3339))))
	})
	shadowMux.HandleFunc("/api/v1/orders", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		time.Sleep(time.Duration(12+rand.Intn(15)) * time.Millisecond)
		// Semantic match: different UUID but ignored by mask
		_, _ = w.Write([]byte(fmt.Sprintf(`{"order_id": "ORD-%d", "total": 99.95, "status": "CONFIRMED", "uuid": "9f8e7d6c-5b4a-4321-8765-fedcba543210"}`, 1000+rand.Intn(9000))))
	})
	shadowMux.HandleFunc("/api/v1/checkout", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		time.Sleep(time.Duration(18+rand.Intn(20)) * time.Millisecond)
		// Missing key: v2 developer forgot auth_code
		_, _ = w.Write([]byte(`{"transaction_id": "tx-881", "success": true}`))
	})
	shadowServer := httptest.NewServer(shadowMux)
	defer shadowServer.Close()

	// 3. Initialize shadowtap components
	telemetry := metrics.NewGlobalTelemetry()
	store := storage.NewRingBuffer(1000)

	dispCfg := proxy.DispatcherConfig{
		ShadowTarget:   shadowServer.URL,
		SampleRate:     1.0,
		QueueCapacity:  2000,
		WorkerCount:    8,
		RequestTimeout: 5 * time.Second,
		PIIConfig:      security.DefaultPIIConfig(),
		MutationConfig: security.DefaultMutationConfig(),
		MaskConfig:     diff.DefaultMaskConfig(),
	}

	dispatcher, err := proxy.NewShadowDispatcher(dispCfg, telemetry, store)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create dispatcher: %v\n", err)
		os.Exit(1)
	}
	defer dispatcher.Stop()

	proxyAddr := fmt.Sprintf(":%d", *proxyPort)
	proxySrv, err := proxy.NewServer(proxy.ProxyServerConfig{
		ListenAddr:    proxyAddr,
		PrimaryTarget: primaryServer.URL,
	}, dispatcher)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create proxy: %v\n", err)
		os.Exit(1)
	}

	// 4. Launch Cockpit Studio
	cockpitSrv := cockpit.NewServer(cockpit.ServerConfig{Port: *cockpitPort}, telemetry, store)
	go func() {
		if err := cockpitSrv.Start(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Cockpit error: %v\n", err)
		}
	}()

	// 5. Launch Proxy
	go func() {
		if err := proxySrv.Start(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Proxy error: %v\n", err)
		}
	}()

	fmt.Println("==================================================================")
	fmt.Println("               shadowtap Live Demo Mode Initialized               ")
	fmt.Println("==================================================================")
	fmt.Printf("• Primary Backend (v1 Mock)  : %s\n", primaryServer.URL)
	fmt.Printf("• Shadow Candidate (v2 Mock) : %s\n", shadowServer.URL)
	fmt.Printf("• shadowtap Reverse Proxy    : http://localhost:%d\n", *proxyPort)
	fmt.Printf("• Cockpit Diff Studio        : http://localhost:%d\n", *cockpitPort)
	fmt.Println("==================================================================")
	fmt.Println("Generating continuous realistic traffic across /users, /orders, /checkout...")
	fmt.Println("Press Ctrl+C to terminate.")

	// 6. Traffic generator
	client := &http.Client{Timeout: 5 * time.Second}
	endpoints := []struct {
		method string
		path   string
		body   string
	}{
		{"GET", "/api/v1/users", ""},
		{"POST", "/api/v1/orders", `{"customer": "John Doe", "card": "4111 2222 3333 4444"}`},
		{"POST", "/api/v1/checkout", `{"amount": 99.95, "currency": "USD"}`},
	}

	ticker := time.NewTicker(600 * time.Millisecond)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-sigCh:
			fmt.Println("\n[Demo] Stopping simulated services...")
			return
		case <-ticker.C:
			ep := endpoints[rand.Intn(len(endpoints))]
			reqURL := fmt.Sprintf("http://localhost:%d%s", *proxyPort, ep.path)
			var bodyReader *strings.Reader
			if ep.body != "" {
				bodyReader = strings.NewReader(ep.body)
			} else {
				bodyReader = strings.NewReader("")
			}
			req, _ := http.NewRequest(ep.method, reqURL, bodyReader)
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}
		}
	}
}
