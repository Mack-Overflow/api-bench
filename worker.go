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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Result struct {
	Latency time.Duration
	Error   bool
	Status  int
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
}

func NewErrorTracker(threshold int, cancel context.CancelFunc) *ErrorTracker {
	return &ErrorTracker{
		threshold: threshold,
		cancel:    cancel,
	}
}

func (e *ErrorTracker) RecordError() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.consecutive++
	if e.consecutive >= e.threshold {
		log.Printf("error threshold reached (%d), stopping benchmark", e.threshold)
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
	maxSuccess int64,
	errorTracker *ErrorTracker,
	limiter <-chan time.Time,
) {
	wctx, err := setupWorker(req)
	if err != nil {
		log.Printf("worker %d setup failed: %v", workerID, err)
		return
	}

	for {
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
		if atomic.LoadInt64(&metrics.SuccessTotal) >= maxSuccess {
			return
		}

		// clone request
		reqCopy := wctx.req.Clone(ctx)

		if len(wctx.body) > 0 {
			reqCopy.Body = io.NopCloser(bytes.NewReader(wctx.body))
		}

		start := time.Now()
		resp, err := wctx.client.Do(reqCopy)
		latency := time.Since(start)

		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, context.Canceled) {
				return
			}
			metrics.record(latency, err)
			log.Printf(err.Error())
			errorTracker.RecordError()
			continue
		}

		bodyBytes, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			log.Printf("failed to read response body: %v", readErr)
		}
		if resp.StatusCode >= 500 {
			head := headLines(bodyBytes, 50, 128*1024)

			log.Printf(
				"HTTP %d SERVER ERROR from %s\n--- first 50 lines ---\n%s\n---------------------",
				resp.StatusCode,
				reqCopy.URL,
				head,
			)

			errorTracker.RecordError()
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfter := resp.Header.Get("Retry-After")

			delay := 2 * time.Second
			if retryAfter != "" {
				if secs, err := strconv.Atoi(retryAfter); err == nil {
					delay = time.Duration(secs) * time.Second
				}
			}

			log.Printf(
				"HTTP 429 from %s — backing off for %s",
				reqCopy.URL,
				delay,
			)
			errorTracker.RecordError()

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
			continue
		}
		if resp.StatusCode >= 400 {
			metrics.record(latency, fmt.Errorf("status %d", resp.StatusCode))

			ct := resp.Header.Get("Content-Type")

			var msg string
			switch {
			case strings.Contains(ct, "text/html"):
				msg = extractHTMLError(bodyBytes)
			case strings.Contains(ct, "application/json"):
				msg = truncate(string(bodyBytes), 256)
			default:
				msg = truncate(string(bodyBytes), 256)
			}

			log.Printf(
				"HTTP %d error from %s: %s",
				resp.StatusCode,
				reqCopy.URL,
				msg,
			)

			errorTracker.RecordError()
			continue
		}

		newTotal := atomic.AddInt64(&metrics.SuccessTotal, 1)

		metrics.record(latency, nil)
		errorTracker.RecordSuccess()

		// stop benchmark exactly at limit
		if newTotal >= maxSuccess {
			return
		}
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
