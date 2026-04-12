package mcp

import (
	"fmt"
	"strings"
	"time"

	"github.com/Mack-Overflow/api-bench/benchmark"
)

// FormatResult produces an LLM-readable summary of benchmark results.
func FormatResult(req benchmark.StartBenchmarkRequest, result *benchmark.BenchmarkResult, stopReason benchmark.StopReason, elapsed time.Duration) string {
	var b strings.Builder

	errPct := float64(0)
	if result.Requests > 0 {
		errPct = float64(result.Errors) / float64(result.Requests) * 100
	}

	rps := float64(0)
	if elapsed.Seconds() > 0 {
		rps = float64(result.Requests) / elapsed.Seconds()
	}

	fmt.Fprintf(&b, "Benchmark completed: %s %s\n", req.Method, req.URL)
	fmt.Fprintf(&b, "Duration: %s | Workers: %d | Stop reason: %s\n\n", elapsed.Truncate(time.Second), req.Concurrency, stopReason)

	fmt.Fprintf(&b, "Requests: %d | Errors: %d (%.1f%%)\n", result.Requests, result.Errors, errPct)
	fmt.Fprintf(&b, "Throughput: %.1f req/s\n\n", rps)

	fmt.Fprintf(&b, "Latency:\n")
	fmt.Fprintf(&b, "  Avg: %.0fms | P50: %dms | P95: %dms | P99: %dms\n", result.AvgMs, result.P50Ms, result.P95Ms, result.P99Ms)
	fmt.Fprintf(&b, "  Min: %.0fms | Max: %.0fms\n\n", result.MinMs, result.MaxMs)

	var codes []string
	if result.Status2xx > 0 {
		codes = append(codes, fmt.Sprintf("2xx=%d", result.Status2xx))
	}
	if result.Status3xx > 0 {
		codes = append(codes, fmt.Sprintf("3xx=%d", result.Status3xx))
	}
	if result.Status4xx > 0 {
		codes = append(codes, fmt.Sprintf("4xx=%d", result.Status4xx))
	}
	if result.Status5xx > 0 {
		codes = append(codes, fmt.Sprintf("5xx=%d", result.Status5xx))
	}
	if len(codes) > 0 {
		fmt.Fprintf(&b, "Status codes: %s\n\n", strings.Join(codes, ", "))
	}

	if result.AvgResponseBytes > 0 {
		fmt.Fprintf(&b, "Response size: Avg %s | Min %s | Max %s\n",
			benchmark.FormatBytes(result.AvgResponseBytes),
			benchmark.FormatBytes(result.MinResponseBytes),
			benchmark.FormatBytes(result.MaxResponseBytes))
	}

	if result.Cache.Hits+result.Cache.Misses > 0 {
		fmt.Fprintf(&b, "\nCache: %d hits, %d misses", result.Cache.Hits, result.Cache.Misses)
		if result.Cache.HitP95Ms > 0 {
			fmt.Fprintf(&b, " | Hit P95: %dms", result.Cache.HitP95Ms)
		}
		if result.Cache.MissP95Ms > 0 {
			fmt.Fprintf(&b, " | Miss P95: %dms", result.Cache.MissP95Ms)
		}
		fmt.Fprintln(&b)
	}

	return b.String()
}

// FormatStatus produces a summary of an active benchmark run's current state.
func FormatStatus(run *benchmark.ActiveRun) string {
	var b strings.Builder

	status := run.GetStatus()
	elapsed := time.Since(run.StartedAt).Truncate(time.Second)

	fmt.Fprintf(&b, "Status: %s | Elapsed: %s\n", status, elapsed)

	if status == benchmark.StatusCompleted {
		if reason := run.GetStopReason(); reason != "" {
			fmt.Fprintf(&b, "Stop reason: %s\n", reason)
		}
	}

	snap, _ := run.SnapshotLogs(0)
	fmt.Fprintf(&b, "Requests: %d | Errors: %d\n", snap.Requests, snap.Errors)
	if snap.P50Ms > 0 {
		fmt.Fprintf(&b, "P50: %dms | P95: %dms\n", snap.P50Ms, snap.P95Ms)
	}

	return b.String()
}
