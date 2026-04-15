package storage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Mack-Overflow/api-bench/benchmark"
)

// StorageBackend is the interface that all storage drivers implement.
type StorageBackend interface {
	SaveRun(ctx context.Context, run BenchmarkRun) error
	ListRuns(ctx context.Context, filter RunFilter) ([]RunSummary, error)
	GetRun(ctx context.Context, id string) (BenchmarkRun, error)
	DeleteRun(ctx context.Context, id string) error
}

// BenchmarkRun is the complete record of a single benchmark execution.
type BenchmarkRun struct {
	ID             string                    `json:"id"`
	Name           string                    `json:"name"`
	URL            string                    `json:"url"`
	Method         string                    `json:"method"`
	Headers        json.RawMessage           `json:"headers,omitempty"`
	Params         json.RawMessage           `json:"params,omitempty"`
	Body           json.RawMessage           `json:"body,omitempty"`
	Concurrency    int                       `json:"concurrency"`
	DurationSec    int                       `json:"duration_seconds"`
	RateLimit      int                       `json:"rate_limit"`
	ThrottleTimeMs int                       `json:"throttle_time_ms"`
	CacheMode      string                    `json:"cache_mode"`
	StartedAt      time.Time                 `json:"started_at"`
	EndedAt        time.Time                 `json:"ended_at"`
	StopReason     string                    `json:"stop_reason"`
	Result         benchmark.BenchmarkResult `json:"result"`
}

// RunSummary is a lightweight view of a benchmark run used by ListRuns.
type RunSummary struct {
	ID         string    `json:"id"`
	URL        string    `json:"url"`
	Method     string    `json:"method"`
	Name       string    `json:"name"`
	StartedAt  time.Time `json:"started_at"`
	Requests   int       `json:"requests"`
	Errors     int       `json:"errors"`
	AvgMs      float64   `json:"avg_ms"`
	P95Ms      int64     `json:"p95_ms"`
	StopReason string    `json:"stop_reason"`
}

// RunFilter controls which runs are returned by ListRuns.
type RunFilter struct {
	Endpoint string     // substring match on URL
	Since    *time.Time // runs started at or after this time
	Before   *time.Time // runs started before this time
	Limit    int        // max results (0 = unlimited)
}

// SummaryFromRun extracts a RunSummary from a full BenchmarkRun.
func SummaryFromRun(r BenchmarkRun) RunSummary {
	return RunSummary{
		ID:         r.ID,
		URL:        r.URL,
		Method:     r.Method,
		Name:       r.Name,
		StartedAt:  r.StartedAt,
		Requests:   r.Result.Requests,
		Errors:     r.Result.Errors,
		AvgMs:      r.Result.AvgMs,
		P95Ms:      r.Result.P95Ms,
		StopReason: r.StopReason,
	}
}
