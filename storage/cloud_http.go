package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// cloudBackend persists benchmark runs by POSTing them to the Laravel API.
// Read methods (ListRuns/GetRun/DeleteRun) are not yet implemented over HTTP.
type cloudBackend struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewCloudBackend returns a StorageBackend that talks to the Laravel API at
// baseURL using the given bmr_* API key as a bearer token.
func NewCloudBackend(baseURL, token string) (*cloudBackend, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("cloud api_url is empty")
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("cloud api token is empty")
	}
	return &cloudBackend{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// SaveRun POSTs the run to /api/runs and returns nil on 2xx.
func (b *cloudBackend) SaveRun(ctx context.Context, run BenchmarkRun) error {
	payload := struct {
		Name           string          `json:"name"`
		URL            string          `json:"url"`
		Method         string          `json:"method"`
		Headers        json.RawMessage `json:"headers,omitempty"`
		Params         json.RawMessage `json:"params,omitempty"`
		Body           json.RawMessage `json:"body,omitempty"`
		Concurrency    int             `json:"concurrency"`
		DurationSec    int             `json:"duration_seconds"`
		RateLimit      int             `json:"rate_limit"`
		ThrottleTimeMs int             `json:"throttle_time_ms"`
		StartedAt      time.Time       `json:"started_at"`
		EndedAt        time.Time       `json:"ended_at"`
		StopReason     string          `json:"stop_reason"`
		Status         string          `json:"status"`
		Result         resultPayload   `json:"result"`
	}{
		Name:           run.Name,
		URL:            run.URL,
		Method:         run.Method,
		Headers:        run.Headers,
		Params:         run.Params,
		Body:           run.Body,
		Concurrency:    run.Concurrency,
		DurationSec:    run.DurationSec,
		RateLimit:      run.RateLimit,
		ThrottleTimeMs: run.ThrottleTimeMs,
		StartedAt:      run.StartedAt,
		EndedAt:        run.EndedAt,
		StopReason:     run.StopReason,
		Status:         "completed",
		Result: resultPayload{
			Requests:         run.Result.Requests,
			Errors:           run.Result.Errors,
			AvgMs:            run.Result.AvgMs,
			P50Ms:            run.Result.P50Ms,
			P95Ms:            run.Result.P95Ms,
			P99Ms:            run.Result.P99Ms,
			MinMs:            run.Result.MinMs,
			MaxMs:            run.Result.MaxMs,
			AvgResponseBytes: run.Result.AvgResponseBytes,
			MinResponseBytes: run.Result.MinResponseBytes,
			MaxResponseBytes: run.Result.MaxResponseBytes,
			Status2xx:        run.Result.Status2xx,
			Status3xx:        run.Result.Status3xx,
			Status4xx:        run.Result.Status4xx,
			Status5xx:        run.Result.Status5xx,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode run payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+"/api/runs", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.token)

	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("post run to %s: %w", req.URL.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("cloud api returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func (b *cloudBackend) ListRuns(ctx context.Context, filter RunFilter) ([]RunSummary, error) {
	u, err := url.Parse(b.baseURL + "/api/runs")
	if err != nil {
		return nil, fmt.Errorf("build list runs URL: %w", err)
	}
	q := u.Query()
	if filter.Endpoint != "" {
		q.Set("endpoint", filter.Endpoint)
	}
	if filter.Version > 0 {
		q.Set("version", strconv.Itoa(filter.Version))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	if filter.Since != nil {
		q.Set("since", filter.Since.Format(time.RFC3339))
	}
	if filter.Before != nil {
		q.Set("before", filter.Before.Format(time.RFC3339))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build list runs request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.token)

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("cloud api returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var result struct {
		Data []RunSummary `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode list runs response: %w", err)
	}
	return result.Data, nil
}

func (b *cloudBackend) GetRun(ctx context.Context, id string) (BenchmarkRun, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.baseURL+"/api/runs/"+id, nil)
	if err != nil {
		return BenchmarkRun{}, fmt.Errorf("build get run request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.token)

	resp, err := b.client.Do(req)
	if err != nil {
		return BenchmarkRun{}, fmt.Errorf("get run: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return BenchmarkRun{}, fmt.Errorf("run %s not found", id)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return BenchmarkRun{}, fmt.Errorf("cloud api returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var run BenchmarkRun
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		return BenchmarkRun{}, fmt.Errorf("decode get run response: %w", err)
	}
	return run, nil
}

func (b *cloudBackend) DeleteRun(ctx context.Context, id string) error {
	return fmt.Errorf("DeleteRun over cloud HTTP is not implemented yet")
}

type resultPayload struct {
	Requests         int     `json:"requests"`
	Errors           int     `json:"errors"`
	AvgMs            float64 `json:"avg_ms"`
	P50Ms            int64   `json:"p50_ms"`
	P95Ms            int64   `json:"p95_ms"`
	P99Ms            int64   `json:"p99_ms"`
	MinMs            float64 `json:"min_ms"`
	MaxMs            float64 `json:"max_ms"`
	AvgResponseBytes int64   `json:"avg_response_bytes"`
	MinResponseBytes int64   `json:"min_response_bytes"`
	MaxResponseBytes int64   `json:"max_response_bytes"`
	Status2xx        int     `json:"status_2xx"`
	Status3xx        int     `json:"status_3xx"`
	Status4xx        int     `json:"status_4xx"`
	Status5xx        int     `json:"status_5xx"`
}
