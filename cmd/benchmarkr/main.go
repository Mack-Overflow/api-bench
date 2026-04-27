package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Mack-Overflow/api-bench/benchmark"
	"github.com/Mack-Overflow/api-bench/config"
	"github.com/Mack-Overflow/api-bench/db"
	"github.com/Mack-Overflow/api-bench/storage"
	"github.com/google/uuid"
)

// version and commit are set by goreleaser via ldflags at build time.
var (
	version = "dev"
	commit  = "none"
)

// stringSlice implements flag.Value for repeatable string flags.
type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ", ") }
func (s *stringSlice) Set(val string) error {
	*s = append(*s, val)
	return nil
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "run":
		err = runCmd(os.Args[2:])
	case "config":
		err = configCmd(os.Args[2:])
	case "endpoints":
		err = endpointsCmd(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Printf("benchmarkr %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`benchmarkr - API performance testing from the command line

Usage:
  benchmarkr <command> [flags]

Commands:
  run         Run a benchmark against a target URL
  config      Manage storage configuration
  endpoints   Manage local endpoint definitions
  version     Print version information
  help        Show this help message

Run "benchmarkr run --help" for benchmark options.
Run "benchmarkr config --help" for configuration options.
Run "benchmarkr endpoints --help" for endpoint management.
`)
}

// --- run command ---

func runCmd(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)

	// Target
	targetURL := fs.String("url", "", "Target URL to benchmark (required if --endpoint not used)")
	method := fs.String("method", "GET", "HTTP method")
	endpointName := fs.String("endpoint", "", "Run a saved endpoint by name (from local config file)")
	fs.StringVar(endpointName, "e", "", "Run a saved endpoint by name (shorthand)")
	endpointVersion := fs.Int("version", 0, "Specific endpoint version to run (requires --endpoint and cloud auth)")
	fs.IntVar(endpointVersion, "v", 0, "Specific endpoint version (shorthand)")
	endpointFile := fs.String("file", "", "Endpoints file (default: discovered from CWD)")

	// Benchmark config
	concurrency := fs.Int("concurrency", 1, "Number of concurrent workers")
	duration := fs.Int("duration", 10, "Test duration in seconds")
	rateLimit := fs.Int("rate-limit", 0, "Max requests per second (0 = unlimited)")
	throttle := fs.Int("throttle", 0, "Per-request delay in milliseconds")
	cacheMode := fs.String("cache-mode", "default", "Cache mode: default, bypass, warm")
	name := fs.String("name", "", "Benchmark name (defaults to URL or endpoint name)")

	// Request options
	var headers stringSlice
	var params stringSlice
	fs.Var(&headers, "header", `HTTP header (repeatable, format: "Key: Value")`)
	fs.Var(&params, "param", `Query parameter (repeatable, format: "key=value")`)
	body := fs.String("body", "", "Request body (JSON string)")

	// Output
	jsonOutput := fs.Bool("json", false, "Output final results as JSON")

	// Storage
	store := fs.Bool("store", false, "Persist results to configured storage backend")
	fs.BoolVar(store, "s", false, "Persist results to configured storage backend (shorthand)")

	fs.Parse(args)

	setFlags := flagsExplicitlySet(fs)

	// Mutually exclusive: --url and --endpoint
	if *targetURL != "" && *endpointName != "" {
		return fmt.Errorf("--url and --endpoint are mutually exclusive")
	}
	if *targetURL == "" && *endpointName == "" {
		fs.Usage()
		return fmt.Errorf("\nprovide --url or --endpoint")
	}
	if *endpointVersion != 0 && *endpointName == "" {
		return fmt.Errorf("--version requires --endpoint")
	}

	// Build the request: from local config (-e) or from raw flags (--url).
	var req benchmark.StartBenchmarkRequest
	if *endpointName != "" {
		built, err := buildRequestFromEndpoint(buildOpts{
			EndpointName:    *endpointName,
			EndpointVersion: *endpointVersion,
			EndpointFile:    *endpointFile,
			Store:           *store,
			Method:          *method,
			Headers:         headers,
			Params:          params,
			Body:            *body,
			Concurrency:     *concurrency,
			Duration:        *duration,
			RateLimit:       *rateLimit,
			Throttle:        *throttle,
			CacheMode:       *cacheMode,
			Name:            *name,
			SetFlags:        setFlags,
		})
		if err != nil {
			return err
		}
		req = built
	} else {
		built, err := buildRequestFromFlags(buildOpts{
			URL:         *targetURL,
			Method:      *method,
			Headers:     headers,
			Params:      params,
			Body:        *body,
			Concurrency: *concurrency,
			Duration:    *duration,
			RateLimit:   *rateLimit,
			Throttle:    *throttle,
			CacheMode:   *cacheMode,
			Name:        *name,
		})
		if err != nil {
			return err
		}
		req = built
	}

	if err := benchmark.ValidateRequest(&req); err != nil {
		return err
	}

	// Pre-validate storage config before starting the benchmark
	var storeCfg *config.Config
	if *store {
		cfg, _, cfgErr := config.Load()
		if cfgErr != nil || !cfg.IsStorageConfigured() {
			return fmt.Errorf("No storage configured. Run 'benchmarkr config init' first")
		}
		storeCfg = cfg
	}

	if !*jsonOutput {
		printBanner(req)
	}

	// Start benchmark directly via the engine
	run := benchmark.Start(req)

	// Handle Ctrl+C: cancel the benchmark
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintf(os.Stderr, "\n  Stopping benchmark...\n")
		run.Cancel()
	}()

	// Stream live metrics until done
	startTime := run.StartedAt
	if !*jsonOutput {
		streamLiveMetrics(run)
	}

	result, stopReason := run.Wait()

	// Persist if --store flag is set (config already validated pre-run)
	if *store {
		if err := persistWithConfig(storeCfg, req, result, stopReason, run.StartedAt); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to persist results: %v\n", err)
		} else if !*jsonOutput {
			fmt.Println("  Results stored.")
		}
	}

	if *jsonOutput {
		out := struct {
			StopReason benchmark.StopReason   `json:"stop_reason"`
			Duration   string                 `json:"duration"`
			Stored     bool                   `json:"stored"`
			Result     *benchmark.BenchmarkResult `json:"result"`
		}{
			StopReason: stopReason,
			Duration:   time.Since(startTime).Truncate(time.Millisecond).String(),
			Stored:     *store,
			Result:     result,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(out)
	} else {
		printResults(result, stopReason, time.Since(startTime))
	}

	return nil
}

// --- Live streaming ---

func streamLiveMetrics(run *benchmark.ActiveRun) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	logCursor := 0
	linesWritten := 0

	for {
		select {
		case <-run.Done():
			// Clear the live display before final results
			clearLines(linesWritten)
			return
		case <-ticker.C:
			snap, cursor := run.SnapshotLogs(logCursor)
			logCursor = cursor

			clearLines(linesWritten)

			elapsed := time.Since(run.StartedAt).Truncate(time.Second)
			linesWritten = printLiveMetrics(snap, elapsed)
		}
	}
}

func clearLines(n int) {
	for i := 0; i < n; i++ {
		fmt.Print("\033[A\033[2K")
	}
}

// --- Storage persistence (--store) ---
func persistWithConfig(cfg *config.Config, req benchmark.StartBenchmarkRequest, result *benchmark.BenchmarkResult, stopReason benchmark.StopReason, startedAt time.Time) error {
	switch cfg.Storage.Mode {
	case "cloud":
		return persistResultCloud(req, result, stopReason)
	case "local":
		backend, err := storage.NewBackendFromConfig(cfg)
		if err != nil {
			return err
		}
		run := storage.BenchmarkRun{
			ID:             uuid.New().String(),
			Name:           req.Name,
			URL:            req.URL,
			Method:         req.Method,
			Headers:        req.Headers,
			Params:         req.Params,
			Body:           req.Body,
			Concurrency:    req.Concurrency,
			DurationSec:    req.DurationSec,
			RateLimit:      req.RateLimit,
			ThrottleTimeMs: req.ThrottleTimeMs,
			CacheMode:      string(req.CacheMode),
			StartedAt:      startedAt,
			EndedAt:        time.Now(),
			StopReason:     string(stopReason),
			Result:         *result,
		}
		return backend.SaveRun(context.Background(), run)
	default:
		return fmt.Errorf("unknown storage mode: %s", cfg.Storage.Mode)
	}
}

func persistResultCloud(req benchmark.StartBenchmarkRequest, result *benchmark.BenchmarkResult, stopReason benchmark.StopReason) error {
	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		return fmt.Errorf("DB_URL environment variable is required for cloud storage mode")
	}

	sqlDB, err := db.OpenDB(dbURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer sqlDB.Close()

	store := db.New(sqlDB)

	_, err = db.PersistBenchmarkResult(store, db.PersistInput{
		Name:              req.Name,
		Method:            req.Method,
		URL:               req.URL,
		Headers:           req.Headers,
		Params:            req.Params,
		Body:              req.Body,
		UserID:            req.UserID,
		Concurrency:       req.Concurrency,
		RateLimit:         req.RateLimit,
		DurationSeconds:   req.DurationSec,
		ThrottleTimeMs:    req.ThrottleTimeMs,
		Status:            "completed",
		StopReason:        string(stopReason),
		EndpointVersionID: req.EndpointVersionID,
		Metrics: db.BenchmarkMetricsInsert{
			Requests:         result.Requests,
			Errors:           result.Errors,
			AvgMs:            result.AvgMs,
			P50Ms:            result.P50Ms,
			P95Ms:            result.P95Ms,
			P99Ms:            result.P99Ms,
			MinMs:            result.MinMs,
			MaxMs:            result.MaxMs,
			AvgResponseBytes: result.AvgResponseBytes,
			MinResponseBytes: result.MinResponseBytes,
			MaxResponseBytes: result.MaxResponseBytes,
			Status2xx:        result.Status2xx,
			Status3xx:        result.Status3xx,
			Status4xx:        result.Status4xx,
			Status5xx:        result.Status5xx,
		},
	})

	return err
}

// --- Terminal output ---

func printBanner(req benchmark.StartBenchmarkRequest) {
	fmt.Println()
	fmt.Println("  Benchmarkr - API Performance Testing")
	fmt.Println("  =====================================")
	fmt.Printf("  Target:      %s %s\n", req.Method, req.URL)
	fmt.Printf("  Workers:     %d\n", req.Concurrency)
	fmt.Printf("  Duration:    %ds\n", req.DurationSec)
	if req.RateLimit > 0 {
		fmt.Printf("  Rate Limit:  %d req/s\n", req.RateLimit)
	}
	if req.ThrottleTimeMs > 0 {
		fmt.Printf("  Throttle:    %dms\n", req.ThrottleTimeMs)
	}
	if req.CacheMode != "default" {
		fmt.Printf("  Cache Mode:  %s\n", string(req.CacheMode))
	}
	fmt.Println()
}

func printLiveMetrics(snap benchmark.MetricsSnapshot, elapsed time.Duration) int {
	lines := 0

	fmt.Printf("  Running... [%s elapsed]\n", elapsed)
	lines++
	fmt.Printf("    Requests:  %d\n", snap.Requests)
	lines++
	fmt.Printf("    Errors:    %d\n", snap.Errors)
	lines++
	if snap.P50Ms > 0 {
		fmt.Printf("    P50:       %dms\n", snap.P50Ms)
		lines++
	}
	if snap.P95Ms > 0 {
		fmt.Printf("    P95:       %dms\n", snap.P95Ms)
		lines++
	}

	return lines
}

func printResults(r *benchmark.BenchmarkResult, stopReason benchmark.StopReason, elapsed time.Duration) {
	fmt.Println()
	fmt.Println("  =====================================")
	fmt.Println("  Results")
	fmt.Println("  =====================================")
	fmt.Println()

	errPct := float64(0)
	if r.Requests > 0 {
		errPct = float64(r.Errors) / float64(r.Requests) * 100
	}

	fmt.Printf("  Total Requests:  %d\n", r.Requests)
	fmt.Printf("  Errors:          %d (%.1f%%)\n", r.Errors, errPct)
	fmt.Printf("  Duration:        %s\n", elapsed.Truncate(time.Second))
	if elapsed.Seconds() > 0 {
		rps := float64(r.Requests) / elapsed.Seconds()
		fmt.Printf("  Throughput:      %.1f req/s\n", rps)
	}
	fmt.Println()

	fmt.Println("  Latency:")
	fmt.Printf("    Avg:  %.0fms\n", r.AvgMs)
	fmt.Printf("    P50:  %dms\n", r.P50Ms)
	fmt.Printf("    P95:  %dms\n", r.P95Ms)
	fmt.Printf("    P99:  %dms\n", r.P99Ms)
	fmt.Printf("    Min:  %.0fms\n", r.MinMs)
	fmt.Printf("    Max:  %.0fms\n", r.MaxMs)
	fmt.Println()

	if r.Status2xx+r.Status3xx+r.Status4xx+r.Status5xx > 0 {
		fmt.Println("  Status Codes:")
		if r.Status2xx > 0 {
			fmt.Printf("    2xx:  %d\n", r.Status2xx)
		}
		if r.Status3xx > 0 {
			fmt.Printf("    3xx:  %d\n", r.Status3xx)
		}
		if r.Status4xx > 0 {
			fmt.Printf("    4xx:  %d\n", r.Status4xx)
		}
		if r.Status5xx > 0 {
			fmt.Printf("    5xx:  %d\n", r.Status5xx)
		}
		fmt.Println()
	}

	if r.AvgResponseBytes > 0 {
		fmt.Println("  Response Size:")
		fmt.Printf("    Avg:  %s\n", benchmark.FormatBytes(r.AvgResponseBytes))
		fmt.Printf("    Min:  %s\n", benchmark.FormatBytes(r.MinResponseBytes))
		fmt.Printf("    Max:  %s\n", benchmark.FormatBytes(r.MaxResponseBytes))
		fmt.Println()
	}

	if r.Cache.Hits+r.Cache.Misses > 0 {
		fmt.Println("  Cache:")
		fmt.Printf("    Hits:   %d\n", r.Cache.Hits)
		fmt.Printf("    Misses: %d\n", r.Cache.Misses)
		if r.Cache.HitP95Ms > 0 {
			fmt.Printf("    Hit P95:  %dms\n", r.Cache.HitP95Ms)
		}
		if r.Cache.MissP95Ms > 0 {
			fmt.Printf("    Miss P95: %dms\n", r.Cache.MissP95Ms)
		}
		fmt.Println()
	}

	fmt.Printf("  Stop Reason:     %s\n", stopReason)
	fmt.Println()
}
