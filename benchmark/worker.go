package benchmark

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

const maxResponseBodyBytes int64 = 512 << 20 // 512 MB

type workerContext struct {
	client *http.Client
	req    *http.Request
	body   []byte
}

type errorTracker struct {
	mu          sync.Mutex
	consecutive int
	threshold   int
	cancel      context.CancelFunc
	metrics     *BenchmarkMetrics
}

func newErrorTracker(threshold int, cancel context.CancelFunc, metrics *BenchmarkMetrics) *errorTracker {
	return &errorTracker{
		threshold: threshold,
		cancel:    cancel,
		metrics:   metrics,
	}
}

func (e *errorTracker) recordError() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.consecutive++
	if e.consecutive >= e.threshold {
		log.Printf("error threshold reached (%d), stopping benchmark", e.threshold)
		if e.metrics != nil {
			e.metrics.AddLog("error", fmt.Sprintf("consecutive error threshold reached (%d), stopping benchmark", e.threshold))
		}
		e.cancel()
	}
}

func (e *errorTracker) recordSuccess() {
	e.mu.Lock()
	e.consecutive = 0
	e.mu.Unlock()
}

func runWorker(
	ctx context.Context,
	workerID int,
	req StartBenchmarkRequest,
	metrics *BenchmarkMetrics,
	et *errorTracker,
	limiter <-chan time.Time,
	throttle time.Duration,
) {
	wctx, err := setupWorker(req)
	if err != nil {
		log.Printf("worker %d setup failed: %v", workerID, err)
		metrics.AddLog("error", fmt.Sprintf("worker %d setup failed: %v", workerID, err))
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

			log.Printf("worker %d: %v", workerID, err)
			metrics.AddLog("error", fmt.Sprintf("worker %d: request failed: %s", workerID, sanitizeError(err)))
			et.recordError()
			metrics.Record(latency, err, 0, 0)
			continue
		}

		statusCode := resp.StatusCode
		var responseSize int64
		var readErr error
		var bodyHead []byte

		// Prefer Content-Length metadata to avoid reading large bodies into memory
		if resp.ContentLength > maxResponseBodyBytes {
			resp.Body.Close()
			finalErr = fmt.Errorf("response exceeds size limit (%d bytes)", resp.ContentLength)
			metrics.AddLog("error", fmt.Sprintf("worker %d: response too large (%d bytes, limit %d)", workerID, resp.ContentLength, maxResponseBodyBytes))
			et.recordError()
			metrics.Record(latency, finalErr, statusCode, resp.ContentLength)
			continue
		}

		// Read body with a hard cap; only buffer content for 5xx diagnostic logging
		limited := io.LimitReader(resp.Body, maxResponseBodyBytes)
		if statusCode >= 500 {
			bodyHead, _ = io.ReadAll(io.LimitReader(limited, 128*1024))
		}
		discarded, discardErr := io.Copy(io.Discard, limited)
		resp.Body.Close()

		responseSize = int64(len(bodyHead)) + discarded
		if resp.ContentLength >= 0 {
			responseSize = resp.ContentLength
		}
		if discardErr != nil {
			readErr = discardErr
		}

		if readErr != nil {
			finalErr = readErr
			metrics.AddLog("warn", fmt.Sprintf("worker %d: failed to read response body", workerID))
		}

		switch {
		case statusCode >= 500:
			finalErr = fmt.Errorf("server error %d", statusCode)

			head := headLines(bodyHead, 50, 128*1024)
			log.Printf(
				"HTTP %d SERVER ERROR from %s\n--- first 50 lines ---\n%s\n---------------------",
				statusCode,
				reqCopy.URL,
				head,
			)
			metrics.AddLog("error", fmt.Sprintf("worker %d: server error HTTP %d", workerID, statusCode))
			et.recordError()

		case statusCode == http.StatusTooManyRequests:
			et.recordError()
			delay := parseRetryAfter(resp)
			metrics.AddLog("warn", fmt.Sprintf("worker %d: rate limited (429), backing off %s", workerID, delay))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}

		case statusCode >= 400:
			finalErr = fmt.Errorf("client error %d", statusCode)
			metrics.AddLog("error", fmt.Sprintf("worker %d: client error HTTP %d", workerID, statusCode))
			et.recordError()

		default:
			atomic.AddInt64(&metrics.SuccessTotal, 1)
			et.recordSuccess()
		}

		metrics.Record(latency, finalErr, statusCode, responseSize)
		et.recordSuccess()

		if throttle > 0 {
			select {
			case <-time.After(throttle):
			case <-ctx.Done():
				return
			}
		}
	}
}

func setupWorker(req StartBenchmarkRequest) (*workerContext, error) {
	parsedURL, err := url.Parse(req.URL)
	if err != nil {
		return nil, err
	}

	bodyBytes := []byte(req.Body)

	httpReq, err := http.NewRequest(req.Method, parsedURL.String(), nil)
	if err != nil {
		return nil, err
	}

	if len(req.Headers) > 0 {
		var headers map[string]string
		if err := json.Unmarshal(req.Headers, &headers); err != nil {
			return nil, fmt.Errorf("invalid headers JSON: %w", err)
		}
		for k, v := range headers {
			httpReq.Header.Set(k, v)
		}
	}

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
