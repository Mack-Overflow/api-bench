package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Mack-Overflow/api-bench/benchmark"
	"github.com/Mack-Overflow/api-bench/db"
	"github.com/mark3labs/mcp-go/mcp"
)

const maxDurationSeconds = 300

// Tools holds the dependencies that tool handlers need.
type Tools struct {
	Registry *RunRegistry
	Store    *db.DB // nil when DB is not configured
}

func (t *Tools) handleRunBenchmark(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	url, err := request.RequireString("url")
	if err != nil {
		return toolError("url is required"), nil
	}

	method := strings.ToUpper(request.GetString("method", "GET"))
	concurrency := request.GetInt("concurrency", 1)
	durationSec := request.GetInt("duration_seconds", 10)
	rateLimit := request.GetInt("rate_limit", 0)
	throttleMs := request.GetInt("throttle_time_ms", 0)
	cacheMode := request.GetString("cache_mode", "default")
	name := request.GetString("name", url)
	store := request.GetBool("store", false)

	if durationSec > maxDurationSeconds {
		durationSec = maxDurationSeconds
	}

	// Build headers JSON from object argument
	var headersJSON json.RawMessage
	if args := request.GetArguments(); args != nil {
		if h, ok := args["headers"]; ok {
			raw, _ := json.Marshal(h)
			headersJSON = raw
		}
	}
	if headersJSON == nil {
		headersJSON = json.RawMessage(`{}`)
	}

	// Build params JSON from object argument
	var paramsJSON json.RawMessage
	if args := request.GetArguments(); args != nil {
		if p, ok := args["params"]; ok {
			raw, _ := json.Marshal(p)
			paramsJSON = raw
		}
	}
	if paramsJSON == nil {
		paramsJSON = json.RawMessage(`{}`)
	}

	// Build body JSON
	bodyStr := request.GetString("body", "")
	var bodyJSON json.RawMessage
	if bodyStr != "" {
		if !json.Valid([]byte(bodyStr)) {
			return toolError("body must be valid JSON"), nil
		}
		bodyJSON = json.RawMessage(bodyStr)
	} else {
		bodyJSON = json.RawMessage(`{}`)
	}

	req := benchmark.StartBenchmarkRequest{
		Name:           name,
		URL:            url,
		Method:         method,
		Headers:        headersJSON,
		Params:         paramsJSON,
		Body:           bodyJSON,
		Concurrency:    concurrency,
		RateLimit:      rateLimit,
		DurationSec:    durationSec,
		ThrottleTimeMs: throttleMs,
		CacheMode:      benchmark.CacheMode(cacheMode),
	}

	if err := benchmark.ValidateRequest(&req); err != nil {
		return toolError(err.Error()), nil
	}

	runID := fmt.Sprintf("%d", time.Now().UnixNano())
	req.RunID = runID

	run := benchmark.StartWithContext(ctx, req)
	t.Registry.Register(runID, run)

	result, stopReason := run.Wait()

	elapsed := time.Since(run.StartedAt)

	// Persist if requested and DB is available
	stored := false
	if store {
		if t.Store == nil {
			// Include a note in the output rather than failing the tool
		} else {
			_, persistErr := db.PersistBenchmarkResult(t.Store, db.PersistInput{
				Name:            req.Name,
				Method:          req.Method,
				URL:             req.URL,
				Headers:         req.Headers,
				Params:          req.Params,
				Body:            req.Body,
				Concurrency:     req.Concurrency,
				RateLimit:       req.RateLimit,
				DurationSeconds: req.DurationSec,
				ThrottleTimeMs:  req.ThrottleTimeMs,
				Status:          "completed",
				StopReason:      string(stopReason),
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
			if persistErr == nil {
				stored = true
			}
		}
	}

	summary := FormatResult(req, result, stopReason, elapsed)
	if store && !stored {
		if t.Store == nil {
			summary += "\nNote: store=true was requested but no database is configured."
		} else {
			summary += "\nNote: store=true was requested but persistence failed."
		}
	} else if stored {
		summary += "\nResults stored to database."
	}

	// Return human-readable summary + raw JSON
	resultJSON, _ := json.MarshalIndent(result, "", "  ")

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: summary},
			mcp.TextContent{Type: "text", Text: string(resultJSON)},
		},
	}, nil
}

func (t *Tools) handleGetBenchmarkStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	runID, err := request.RequireString("run_id")
	if err != nil {
		return toolError("run_id is required"), nil
	}

	run, err := t.Registry.Get(runID)
	if err != nil {
		return toolError(err.Error()), nil
	}

	summary := FormatStatus(run)

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: summary},
		},
	}, nil
}

func (t *Tools) handleStopBenchmark(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	runID, err := request.RequireString("run_id")
	if err != nil {
		return toolError("run_id is required"), nil
	}

	run, err := t.Registry.Get(runID)
	if err != nil {
		return toolError(err.Error()), nil
	}

	run.Cancel()

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: fmt.Sprintf("Benchmark %s is being stopped.", runID)},
		},
	}, nil
}

func (t *Tools) handleListEndpoints(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if t.Store == nil {
		return toolError("database is not configured — set DB_URL to use this tool"), nil
	}

	limit := request.GetInt("limit", 20)
	offset := request.GetInt("offset", 0)

	endpoints, err := t.Store.ListEndpoints(limit, offset)
	if err != nil {
		return toolError(fmt.Sprintf("query failed: %v", err)), nil
	}

	if len(endpoints) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{Type: "text", Text: "No saved endpoints found."},
			},
		}, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Saved endpoints (%d results):\n\n", len(endpoints))
	for _, e := range endpoints {
		fmt.Fprintf(&b, "  [%d] %s %s — %s (created %s)\n", e.ID, e.Method, e.URL, e.Name, e.CreatedAt.Format("2006-01-02"))
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: b.String()},
		},
	}, nil
}

func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: msg},
		},
		IsError: true,
	}
}
