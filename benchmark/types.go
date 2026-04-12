package benchmark

import (
	"encoding/json"
	"fmt"
	"strings"
)

type BenchmarkStatus string

const (
	StatusPending   BenchmarkStatus = "pending"
	StatusRunning   BenchmarkStatus = "running"
	StatusCompleted BenchmarkStatus = "completed"
	StatusFailed    BenchmarkStatus = "failed"
)

type StopReason string

const (
	StopCompleted StopReason = "completed"
	StopCanceled  StopReason = "canceled"
	StopErrors    StopReason = "consecutive_errors"
)

type CacheMode string

const (
	CacheDefault CacheMode = "default"
	CacheBypass  CacheMode = "bypass"
	CacheWarm    CacheMode = "warm"
)

type StartBenchmarkRequest struct {
	RunID             string `json:"run_id"`
	EndpointID        *int64 `json:"endpoint_id"`
	EndpointVersionID *int64 `json:"endpoint_version_id,omitempty"`
	ChangesMade       bool   `json:"changes_made"`

	UserID *int64 `json:"user_id,omitempty"`

	Name    string          `json:"name"`
	URL     string          `json:"url"`
	Method  string          `json:"method"`
	Headers json.RawMessage `json:"headers"`
	Params  json.RawMessage `json:"params"`
	Body    json.RawMessage `json:"body"`

	Concurrency    int       `json:"concurrency"`
	RateLimit      int       `json:"rate_limit"`
	DurationSec    int       `json:"duration_seconds"`
	ThrottleTimeMs int       `json:"throttle_time_ms"`
	CacheMode      CacheMode `json:"cache_mode"`
}

type BenchmarkResult struct {
	Requests int     `json:"requests"`
	Errors   int     `json:"errors_total"`
	AvgMs    float64 `json:"avg_ms"`
	P50Ms    int64   `json:"p50_ms"`
	P95Ms    int64   `json:"p95_ms"`
	P99Ms    int64   `json:"p99_ms"`
	MinMs    float64 `json:"min_ms"`
	MaxMs    float64 `json:"max_ms"`

	AvgResponseBytes int64 `json:"avg_response_bytes"`
	MinResponseBytes int64 `json:"min_response_bytes"`
	MaxResponseBytes int64 `json:"max_response_bytes"`

	Status2xx int `json:"status_2xx"`
	Status3xx int `json:"status_3xx"`
	Status4xx int `json:"status_4xx"`
	Status5xx int `json:"status_5xx"`

	Cache CacheResult `json:"cache"`
}

type CacheResult struct {
	Hits      int   `json:"hits"`
	Misses    int   `json:"misses"`
	HitP95Ms  int64 `json:"hit_p95_ms,omitempty"`
	MissP95Ms int64 `json:"miss_p95_ms,omitempty"`
}

func ValidateRequest(req *StartBenchmarkRequest) error {
	if req.URL == "" {
		return fmt.Errorf("url is required")
	}
	if req.Method == "" {
		return fmt.Errorf("method is required")
	}
	if req.Concurrency <= 0 {
		return fmt.Errorf("concurrency must be > 0")
	}
	if req.DurationSec <= 0 {
		return fmt.Errorf("duration_seconds must be > 0")
	}
	return nil
}

func (r StartBenchmarkRequest) String() string {
	var b strings.Builder

	b.WriteString("BenchmarkRequest{\n")
	b.WriteString(fmt.Sprintf("  URL: %s\n", r.URL))
	b.WriteString(fmt.Sprintf("  Method: %s\n", r.Method))
	b.WriteString(fmt.Sprintf("  Concurrency: %d\n", r.Concurrency))
	b.WriteString(fmt.Sprintf("  DurationSec: %d\n", r.DurationSec))
	b.WriteString(fmt.Sprintf("  ThrottleTimeMs: %d\n", r.ThrottleTimeMs))

	if len(r.Headers) > 0 {
		var headers map[string]string
		if err := json.Unmarshal(r.Headers, &headers); err == nil {
			b.WriteString("  Headers:\n")
			for k, v := range headers {
				b.WriteString(fmt.Sprintf("    %s: %s\n", k, redactHeader(k, v)))
			}
		}
	}

	if len(r.Params) > 0 {
		var params map[string]string
		if err := json.Unmarshal(r.Params, &params); err == nil {
			b.WriteString("  Params:\n")
			for k, v := range params {
				b.WriteString(fmt.Sprintf("    %s=%s\n", k, v))
			}
		}
	}

	if len(r.Body) > 0 {
		b.WriteString("  Body: ")
		b.WriteString(bodySummary(r.Body))
		b.WriteString("\n")
	}

	b.WriteString("}")

	return b.String()
}
