package mcp

import (
	"github.com/Mack-Overflow/api-bench/config"
	"github.com/Mack-Overflow/api-bench/db"
	"github.com/Mack-Overflow/api-bench/storage"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const Version = "0.1.0"

// NewServer creates a configured MCP server with all benchmarking tools registered.
// backend is the configured storage.StorageBackend (nil if --store is unusable).
// cfg is the loaded benchmarkr config (nil if no config). Used by the preflight
// check to know whether to call Laravel before running.
// store is the SQL DB used by read-side helpers (list_endpoints).
func NewServer(backend storage.StorageBackend, cfg *config.Config, store *db.DB) *server.MCPServer {
	s := server.NewMCPServer(
		"benchmarkr",
		Version,
		server.WithToolCapabilities(true),
	)

	tools := &Tools{
		Registry: NewRunRegistry(),
		Backend:  backend,
		Config:   cfg,
		Store:    store,
	}

	// run_benchmark — run an API performance benchmark and return results
	s.AddTool(mcp.NewTool("run_benchmark",
		mcp.WithDescription("Run an API performance benchmark against a target URL. Starts the benchmark, waits for completion, and returns full results including latency percentiles, throughput, error rates, and status code distribution. Duration is capped at 300 seconds."),
		mcp.WithString("url",
			mcp.Description("Target URL to benchmark"),
			mcp.Required(),
		),
		mcp.WithString("method",
			mcp.Description("HTTP method (GET, POST, PUT, DELETE, etc.)"),
			mcp.DefaultString("GET"),
		),
		mcp.WithNumber("concurrency",
			mcp.Description("Number of concurrent workers sending requests"),
			mcp.DefaultNumber(1),
			mcp.Min(1),
		),
		mcp.WithNumber("duration_seconds",
			mcp.Description("How long to run the benchmark in seconds (max 300)"),
			mcp.DefaultNumber(10),
			mcp.Min(1),
			mcp.Max(300),
		),
		mcp.WithNumber("rate_limit",
			mcp.Description("Maximum requests per second (0 = unlimited)"),
			mcp.DefaultNumber(0),
		),
		mcp.WithNumber("throttle_time_ms",
			mcp.Description("Delay in milliseconds between each request per worker"),
			mcp.DefaultNumber(0),
		),
		mcp.WithString("cache_mode",
			mcp.Description("Cache mode: default (normal), bypass (add cache-busting headers), warm (pre-warm cache before benchmark)"),
			mcp.DefaultString("default"),
			mcp.Enum("default", "bypass", "warm"),
		),
		mcp.WithObject("headers",
			mcp.Description("HTTP headers as key-value pairs, e.g. {\"Authorization\": \"Bearer token\"}"),
		),
		mcp.WithObject("params",
			mcp.Description("Query parameters as key-value pairs, e.g. {\"page\": \"1\"}"),
		),
		mcp.WithString("body",
			mcp.Description("Request body as a JSON string"),
		),
		mcp.WithString("name",
			mcp.Description("Name for this benchmark (defaults to URL)"),
		),
		mcp.WithBoolean("store",
			mcp.Description("Persist results to database (requires DB_URL)"),
			mcp.DefaultBool(false),
		),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:        "Run Benchmark",
			OpenWorldHint: boolPtr(true),
		}),
	), tools.handleRunBenchmark)

	// get_benchmark_status — check an active or completed benchmark run
	s.AddTool(mcp.NewTool("get_benchmark_status",
		mcp.WithDescription("Check the status and current metrics of a benchmark run. Use the run_id returned by run_benchmark."),
		mcp.WithString("run_id",
			mcp.Description("The benchmark run ID"),
			mcp.Required(),
		),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:         "Get Benchmark Status",
			ReadOnlyHint:  boolPtr(true),
		}),
	), tools.handleGetBenchmarkStatus)

	// stop_benchmark — cancel a running benchmark
	s.AddTool(mcp.NewTool("stop_benchmark",
		mcp.WithDescription("Cancel a running benchmark. The benchmark will stop and return partial results."),
		mcp.WithString("run_id",
			mcp.Description("The benchmark run ID to cancel"),
			mcp.Required(),
		),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:           "Stop Benchmark",
			DestructiveHint: boolPtr(true),
		}),
	), tools.handleStopBenchmark)

	// list_endpoints — query saved API endpoints from the database
	s.AddTool(mcp.NewTool("list_endpoints",
		mcp.WithDescription("List saved API endpoints from the database. Requires a database connection (DB_URL). Results are ordered by most recently created."),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of endpoints to return"),
			mcp.DefaultNumber(20),
		),
		mcp.WithNumber("offset",
			mcp.Description("Number of endpoints to skip (for pagination)"),
			mcp.DefaultNumber(0),
		),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:        "List Endpoints",
			ReadOnlyHint: boolPtr(true),
		}),
	), tools.handleListEndpoints)

	return s
}

func boolPtr(b bool) *bool {
	return &b
}
