package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Mack-Overflow/api-bench/benchmark"
	"github.com/Mack-Overflow/api-bench/config"
	"github.com/Mack-Overflow/api-bench/storage"
)

const (
	ansiReset     = "\033[0m"
	ansiRed       = "\033[31m"
	ansiDarkGreen = "\033[32m"
	ansiCyan      = "\033[36m"
	ansiBold      = "\033[1m"
	ansiDim       = "\033[2m"
)

var historyColors bool

func colorize(text, code string) string {
	if !historyColors {
		return text
	}
	return code + text + ansiReset
}

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func historyCmd(args []string) error {
	fs := flag.NewFlagSet("history", flag.ExitOnError)
	lookback := fs.Int("lookback", 0, "Show the N most recent runs (default: 1 with no URL, 10 with URL)")
	version := fs.Int("version", 0, "Filter to a specific endpoint version (cloud DB only; not supported for JSON storage)")
	id := fs.String("id", "", "Show full detail for a single run by ID")
	jsonOutput := fs.Bool("json", false, "Emit JSON instead of pretty table")
	noColor := fs.Bool("no-color", false, "Disable ANSI colors")
	since := fs.String("since", "", "Show runs started at or after this time (ISO8601, e.g. 2026-05-01T00:00:00Z)")
	before := fs.String("before", "", "Show runs started before this time (ISO8601)")

	fs.Usage = func() {
		fmt.Print(`benchmarkr history - Browse and inspect past benchmark runs

Usage:
  benchmarkr history [endpoint-url] [flags]

Flags:
  --lookback N    Show the N most recent runs (default: 1 with no URL, 10 with URL)
  --version V     Filter to a specific endpoint version (cloud DB only)
  --id ID         Show full detail for a single run by ID
  --json          Emit JSON instead of pretty table
  --no-color      Disable ANSI colors
  --since TIME    Show runs started at or after this time (ISO8601)
  --before TIME   Show runs started before this time (ISO8601)

Note: --version is not supported for JSON storage.
`)
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	endpointURL := ""
	if fs.NArg() > 0 {
		endpointURL = fs.Arg(0)
	}

	effectiveLookback := *lookback
	if effectiveLookback == 0 {
		if endpointURL != "" {
			effectiveLookback = 10
		} else {
			effectiveLookback = 1
		}
	}

	historyColors = !*noColor &&
		os.Getenv("NO_COLOR") == "" &&
		isTTY()
	if *jsonOutput {
		historyColors = false
	}

	cfg, source, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if !*jsonOutput {
		printHistorySource(cfg, source)
	}

	backend, err := storage.NewBackendFromConfig(cfg)
	if err != nil {
		return err
	}

	ctx := context.Background()

	if *id != "" {
		run, err := backend.GetRun(ctx, *id)
		if err != nil {
			return err
		}
		if *jsonOutput {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(run)
		}
		printRunDetail(run)
		return nil
	}

	filter := storage.RunFilter{
		Endpoint: endpointURL,
		Limit:    effectiveLookback,
		Version:  *version,
	}
	if *since != "" {
		t, err := time.Parse(time.RFC3339, *since)
		if err != nil {
			return fmt.Errorf("invalid --since %q: must be ISO8601 (e.g. 2026-05-01T00:00:00Z)", *since)
		}
		filter.Since = &t
	}
	if *before != "" {
		t, err := time.Parse(time.RFC3339, *before)
		if err != nil {
			return fmt.Errorf("invalid --before %q: must be ISO8601", *before)
		}
		filter.Before = &t
	}

	runs, err := backend.ListRuns(ctx, filter)
	if err != nil {
		return err
	}

	if len(runs) == 0 {
		fmt.Fprintln(os.Stderr, "  No runs found.")
		return nil
	}

	// No positional URL: detail view of the single newest run
	if endpointURL == "" {
		run, err := backend.GetRun(ctx, runs[0].ID)
		if err != nil {
			return err
		}
		if *jsonOutput {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(run)
		}
		printRunDetail(run)
		return nil
	}

	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(runs)
	}
	printRunList(runs, endpointURL)
	return nil
}

func printHistorySource(cfg *config.Config, _ *config.Source) {
	switch cfg.Storage.Mode {
	case "cloud":
		fmt.Fprintf(os.Stderr, "  Source: cloud (%s)\n", cfg.Cloud.API_URL)
	case "local":
		switch cfg.Storage.Local.Driver {
		case "json":
			dir := config.ExpandPath(cfg.Storage.Local.JSON.OutputDir)
			fmt.Fprintf(os.Stderr, "  Source: local json (%s)\n", dir)
		case "postgres":
			pg := cfg.Storage.Local.Postgres
			fmt.Fprintf(os.Stderr, "  Source: local postgres (%s:%d/%s)\n", pg.Host, pg.Port, pg.Database)
		case "mysql":
			my := cfg.Storage.Local.MySQL
			fmt.Fprintf(os.Stderr, "  Source: local mysql (%s:%d/%s)\n", my.Host, my.Port, my.Database)
		}
	}
}

// padRight pads s with spaces on the right to reach width.
func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// colorField pads the plain value to width then wraps it in ANSI codes.
// The padding is applied before colorizing so terminal width accounting is correct.
func colorField(val string, width int, code string) string {
	padding := strings.Repeat(" ", max(0, width-len(val)))
	return colorize(val, code) + padding
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func printRunList(runs []storage.RunSummary, endpoint string) {
	fmt.Fprintf(os.Stderr, "  Endpoint: %s  (%d runs)\n\n", endpoint, len(runs))

	type rowData struct {
		started  string
		name     string
		method   string
		url      string
		req      string
		err      string
		success  string
		avg      string
		p95      string
		size     string
		stop     string
		hasErr   bool
		badStop  bool
	}

	headers := []string{"STARTED", "NAME", "METHOD", "URL", "REQ", "ERR", "SUCCESS", "AVG", "P95", "SIZE", "STOP"}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}

	rows := make([]rowData, len(runs))
	for i, r := range runs {
		successPct := float64(0)
		if r.Requests > 0 {
			successPct = float64(r.Requests-r.Errors) / float64(r.Requests) * 100
		}
		sizeStr := "-"
		if r.AvgResponseBytes > 0 {
			sizeStr = benchmark.FormatBytes(r.AvgResponseBytes)
		}
		rows[i] = rowData{
			started: r.StartedAt.Local().Format("2006-01-02 15:04:05"),
			name:    r.Name,
			method:  r.Method,
			url:     r.URL,
			req:     fmt.Sprintf("%d", r.Requests),
			err:     fmt.Sprintf("%d", r.Errors),
			success: fmt.Sprintf("%.2f%%", successPct),
			avg:     fmt.Sprintf("%.0fms", r.AvgMs),
			p95:     fmt.Sprintf("%dms", r.P95Ms),
			size:    sizeStr,
			stop:    r.StopReason,
			hasErr:  r.Errors > 0,
			badStop: r.StopReason != "" && r.StopReason != "completed",
		}
		fields := []string{
			rows[i].started, rows[i].name, rows[i].method, rows[i].url,
			rows[i].req, rows[i].err, rows[i].success, rows[i].avg,
			rows[i].p95, rows[i].size, rows[i].stop,
		}
		for j, f := range fields {
			if len(f) > widths[j] {
				widths[j] = len(f)
			}
		}
	}

	// Header line
	headerParts := make([]string, len(headers))
	for i, h := range headers {
		headerParts[i] = padRight(h, widths[i])
	}
	fmt.Println(colorize("  "+strings.Join(headerParts, "  "), ansiBold))

	// Data rows
	for _, r := range rows {
		parts := []string{
			padRight(r.started, widths[0]),
			padRight(r.name, widths[1]),
			padRight(r.method, widths[2]),
			padRight(r.url, widths[3]),
			padRight(r.req, widths[4]),
		}

		// ERR: red when > 0
		if r.hasErr {
			parts = append(parts, colorField(r.err, widths[5], ansiRed))
		} else {
			parts = append(parts, padRight(r.err, widths[5]))
		}

		// SUCCESS: always dark green
		parts = append(parts, colorField(r.success, widths[6], ansiDarkGreen))

		parts = append(parts,
			padRight(r.avg, widths[7]),
			padRight(r.p95, widths[8]),
		)

		// SIZE: cyan
		parts = append(parts, colorField(r.size, widths[9], ansiCyan))

		// STOP: dim for "completed", red otherwise
		if r.badStop {
			parts = append(parts, colorize(r.stop, ansiRed))
		} else {
			parts = append(parts, colorize(r.stop, ansiDim))
		}

		fmt.Println("  " + strings.Join(parts, "  "))
	}
}

func printRunDetail(run storage.BenchmarkRun) {
	label := func(s string) string { return colorize(s, ansiBold) }

	fmt.Println()
	fmt.Println(colorize("  =====================================", ansiBold))
	fmt.Println(colorize("  Run Details", ansiBold))
	fmt.Println(colorize("  =====================================", ansiBold))
	fmt.Println()
	fmt.Printf("  %s %s\n", label("ID:"), run.ID)
	fmt.Printf("  %s %s\n", label("Name:"), run.Name)
	fmt.Printf("  %s %s\n", label("Started:"), run.StartedAt.Local().Format("2006-01-02 15:04:05"))
	fmt.Printf("  %s %s %s\n", label("Target:"), run.Method, run.URL)
	fmt.Printf("  %s %s\n", label("Stop Reason:"), run.StopReason)
	fmt.Println()

	r := &run.Result
	elapsed := run.EndedAt.Sub(run.StartedAt)

	errPct := float64(0)
	successPct := float64(100)
	if r.Requests > 0 {
		errPct = float64(r.Errors) / float64(r.Requests) * 100
		successPct = float64(r.Requests-r.Errors) / float64(r.Requests) * 100
	}

	fmt.Printf("  %s %d\n", label("Total Requests:"), r.Requests)

	errLine := fmt.Sprintf("  %s %d (%.1f%%)", label("Errors:"), r.Errors, errPct)
	if r.Errors > 0 && historyColors {
		errLine = ansiRed + errLine + ansiReset
	}
	fmt.Println(errLine)

	fmt.Printf("  %s %s\n", label("Success Rate:"), colorize(fmt.Sprintf("%.2f%%", successPct), ansiDarkGreen))
	fmt.Printf("  %s %s\n", label("Duration:"), elapsed.Truncate(time.Second))
	if elapsed.Seconds() > 0 {
		rps := float64(r.Requests) / elapsed.Seconds()
		fmt.Printf("  %s %.1f req/s\n", label("Throughput:"), rps)
	}
	fmt.Println()

	fmt.Printf("  %s\n", label("Latency:"))
	fmt.Printf("    Avg:  %.0fms\n", r.AvgMs)
	fmt.Printf("    P50:  %dms\n", r.P50Ms)
	fmt.Printf("    P95:  %dms\n", r.P95Ms)
	fmt.Printf("    P99:  %dms\n", r.P99Ms)
	fmt.Printf("    Min:  %.0fms\n", r.MinMs)
	fmt.Printf("    Max:  %.0fms\n", r.MaxMs)
	fmt.Println()

	if r.Status2xx+r.Status3xx+r.Status4xx+r.Status5xx > 0 {
		fmt.Printf("  %s\n", label("Status Codes:"))
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
		fmt.Printf("  %s\n", label("Response Size:"))
		fmt.Printf("    Avg:  %s\n", colorize(benchmark.FormatBytes(r.AvgResponseBytes), ansiCyan))
		fmt.Printf("    Min:  %s\n", colorize(benchmark.FormatBytes(r.MinResponseBytes), ansiCyan))
		fmt.Printf("    Max:  %s\n", colorize(benchmark.FormatBytes(r.MaxResponseBytes), ansiCyan))
		fmt.Println()
	}

	if r.Cache.Hits+r.Cache.Misses > 0 {
		fmt.Printf("  %s\n", label("Cache:"))
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
}
