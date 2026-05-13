package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mack-Overflow/api-bench/benchmark"
	"github.com/Mack-Overflow/api-bench/config"
	"github.com/Mack-Overflow/api-bench/db"
	"github.com/Mack-Overflow/api-bench/storage"
	"github.com/mark3labs/mcp-go/mcp"
)

const maxDurationSeconds = 300

const (
	settingsURL = "https://benchmarkr-1.onrender.com/settings"
	billingURL  = "https://benchmarkr-1.onrender.com/settings#billing"
)

// Tools holds the dependencies that tool handlers need.
//
// Backend is the configured storage.StorageBackend (the single write path for
// completed runs). Config is the loaded benchmarkr config — when its storage
// mode is "cloud", run_benchmark calls Laravel's preflight endpoint before
// starting. Store is kept only for read-side helpers such as list_endpoints.
type Tools struct {
	Registry *RunRegistry
	Backend  storage.StorageBackend // nil when no storage is configured
	Config   *config.Config         // nil when no config is loaded
	Store    *db.DB                 // nil when DB is not configured
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

	// Preflight against Laravel when storing to the cloud backend. Bad API
	// key blocks the run; storage cap downgrades store to false but lets
	// the benchmark proceed.
	disableStorage := false
	var preflightNote string
	if store && t.Config != nil && t.Config.Storage.Mode == "cloud" {
		pf, err := storage.CloudPreflight(ctx, t.Config, storage.PreflightOpts{})
		switch {
		case errors.Is(err, storage.ErrAuth):
			return toolError(fmt.Sprintf(
				"your API key is missing or invalid.\n  1. Visit %s to generate a new one.\n  2. Run: benchmarkr config set cloud.token <KEY>",
				settingsURL,
			)), nil
		case err != nil:
			return toolError(fmt.Sprintf("preflight failed: %v", err)), nil
		case !pf.Storage.Allowed:
			disableStorage = true
			preflightNote = fmt.Sprintf(
				"Stored-runs limit reached (%d/%d). Benchmark will run but won't be saved. Upgrade at %s",
				pf.Storage.Stored, pf.Storage.Limit, billingURL,
			)
		}
	}

	runID := fmt.Sprintf("%d", time.Now().UnixNano())
	req.RunID = runID

	run := benchmark.StartWithContext(ctx, req)
	t.Registry.Register(runID, run)

	result, stopReason := run.Wait()

	elapsed := time.Since(run.StartedAt)

	// Persist if requested and a backend is configured
	stored := false
	var persistErr error
	if store && !disableStorage {
		if t.Backend == nil {
			persistErr = fmt.Errorf("no storage backend configured")
		} else {
			persistErr = t.Backend.SaveRun(ctx, storage.BenchmarkRun{
				ID:             runID,
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
				StartedAt:      run.StartedAt,
				EndedAt:        time.Now(),
				StopReason:     string(stopReason),
				Result:         *result,
			})
			if persistErr == nil {
				stored = true
			}
		}
	}

	summary := FormatResult(req, result, stopReason, elapsed)
	if preflightNote != "" {
		summary += "\n" + preflightNote
	}
	if store && !stored && !disableStorage {
		if t.Backend == nil {
			summary += "\nNote: store=true was requested but no storage backend is configured."
		} else {
			summary += fmt.Sprintf("\nNote: store=true was requested but persistence failed: %v", persistErr)
		}
	} else if stored {
		summary += "\nResults stored."
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
