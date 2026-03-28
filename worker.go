package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

type Result struct {
	Latency  time.Duration
	Error    bool
	Status   int
	CacheHit *bool
}

type workerContext struct {
	client *http.Client
	req    *http.Request
	body   []byte
}

type ErrorTracker struct {
	mu sync.Mutex

	consecutive int
	threshold   int
	cancel      context.CancelFunc
	metrics     *BenchmarkMetrics
}

func NewErrorTracker(threshold int, cancel context.CancelFunc, metrics *BenchmarkMetrics) *ErrorTracker {
	return &ErrorTracker{
		threshold: threshold,
		cancel:    cancel,
		metrics:   metrics,
	}
}

func (e *ErrorTracker) RecordError() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.consecutive++
	if e.consecutive >= e.threshold {
		log.Printf("error threshold reached (%d), stopping benchmark", e.threshold)
		if e.metrics != nil {
			e.metrics.addLog("error", fmt.Sprintf("consecutive error threshold reached (%d), stopping benchmark", e.threshold))
		}
		e.cancel()
	}
}

func (e *ErrorTracker) RecordSuccess() {
	e.mu.Lock()
	e.consecutive = 0
	e.mu.Unlock()
}

func worker(ctx context.Context, client *http.Client, jobs <-chan struct{}, results chan<- Result, req *http.Request) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-jobs:
			start := time.Now()
			resp, err := client.Do(req.Clone(ctx))
			latency := time.Since(start)

			if err != nil {
				results <- Result{Latency: latency, Error: true}
				continue
			}

			resp.Body.Close()
			results <- Result{
				Latency: latency,
				Status:  resp.StatusCode,
			}
		}
	}
}

func benchmarkWorker(
	ctx context.Context,
	workerID int,
	req StartBenchmarkRequest,
	metrics *BenchmarkMetrics,
	errorTracker *ErrorTracker,
	limiter <-chan time.Time,
) {
	wctx, err := setupWorker(req)
	if err != nil {
		log.Printf("worker %d setup failed: %v", workerID, err)
		metrics.addLog("error", fmt.Sprintf("worker %d setup failed: %v", workerID, err))
		return
	}

	for {
		log.Printf("worker %d tick", workerID)
		select {
		case <-ctx.Done():
			return
		default:
		}

		if limiter != nil {
			select {
			case <-limiter:
			case <-ctx.Done():
				return
			}
		}

		// clone request
		reqCopy := wctx.req.Clone(ctx)

		if len(wctx.body) > 0 {
			reqCopy.Body = io.NopCloser(bytes.NewReader(wctx.body))
		}

		start := time.Now()
		resp, err := wctx.client.Do(reqCopy)
		latency := time.Since(start)

		var finalErr error

		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return
			}

			log.Printf(err.Error())
			metrics.addLog("error", fmt.Sprintf("worker %d: request failed: %s", workerID, sanitizeError(err)))
			errorTracker.RecordError()
			metrics.record(latency, err)
			continue
		}

		bodyBytes, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if readErr != nil {
			finalErr = readErr
			metrics.addLog("warn", fmt.Sprintf("worker %d: failed to read response body", workerID))
		}
		switch {
		case resp.StatusCode >= 500:
			finalErr = fmt.Errorf("server error %d", resp.StatusCode)

			head := headLines(bodyBytes, 50, 128*1024)
			log.Printf(
				"HTTP %d SERVER ERROR from %s\n--- first 50 lines ---\n%s\n---------------------",
				resp.StatusCode,
				reqCopy.URL,
				head,
			)
			metrics.addLog("error", fmt.Sprintf("worker %d: server error HTTP %d", workerID, resp.StatusCode))
			errorTracker.RecordError()

		case resp.StatusCode == http.StatusTooManyRequests:
			errorTracker.RecordError()
			delay := parseRetryAfter(resp)
			metrics.addLog("warn", fmt.Sprintf("worker %d: rate limited (429), backing off %s", workerID, delay))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}

		case resp.StatusCode >= 400:
			finalErr = fmt.Errorf("client error %d", resp.StatusCode)
			metrics.addLog("error", fmt.Sprintf("worker %d: client error HTTP %d", workerID, resp.StatusCode))
			errorTracker.RecordError()

		default:
			atomic.AddInt64(&metrics.SuccessTotal, 1)
			errorTracker.RecordSuccess()
		}

		metrics.record(latency, finalErr)
		// cacheHit := detectCacheHit(resp.Header)
		// metrics.recordWithCache(latency, nil, cacheHit)
		errorTracker.RecordSuccess()
	}
}

func setupWorker(req StartBenchmarkRequest) (*workerContext, error) {
	parsedURL, err := url.Parse(req.URL)
	if err != nil {
		return nil, err
	}

	// req.Body is already []byte (json.RawMessage), no need to marshal
	bodyBytes := []byte(req.Body)

	httpReq, err := http.NewRequest(req.Method, parsedURL.String(), nil)
	if err != nil {
		return nil, err
	}

	// Unmarshal headers from JSON
	if len(req.Headers) > 0 {
		var headers map[string]string
		if err := json.Unmarshal(req.Headers, &headers); err != nil {
			return nil, fmt.Errorf("invalid headers JSON: %w", err)
		}
		for k, v := range headers {
			httpReq.Header.Set(k, v)
		}
	}

	// Unmarshal params from JSON
	if len(req.Params) > 0 {
		var params map[string]string
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, fmt.Errorf("invalid params JSON: %w", err)
		}
		q := httpReq.URL.Query()
		for k, v := range params {
			q.Set(k, v)
		}
		httpReq.URL.RawQuery = q.Encode()
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	switch req.CacheMode {
	case CacheBypass:
		httpReq.Header.Set("Cache-Control", "no-cache, no-store, max-age=0")
		httpReq.Header.Set("Pragma", "no-cache")
	case CacheWarm, CacheDefault:
		// no-op
	}

	return &workerContext{
		client: client,
		req:    httpReq,
		body:   bodyBytes,
	}, nil
}

func newRateLimiter(rps int) <-chan time.Time {
	if rps <= 0 {
		return nil
	}
	ticker := time.NewTicker(time.Second / time.Duration(rps))
	return ticker.C
}
