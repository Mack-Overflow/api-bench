package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/Mack-Overflow/api-bench/db"
)

type BenchmarkStatusResponse struct {
	ID         string          `json:"id"`
	Status     BenchmarkStatus `json:"status"`
	StartedAt  time.Time       `json:"started_at"`
	EndedAt    *time.Time      `json:"ended_at,omitempty"`
	Requests   int64           `json:"requests"`
	Errors     int64           `json:"errors"`
	P50Ms      int64           `json:"p50_ms"`
	P95Ms      int64           `json:"p95_ms"`
	StopReason StopReason      `json:"stop_reason,omitempty"`
}

func startBenchmarkHandler(store *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req StartBenchmarkRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := validateStartRequest(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.EndpointID != nil && *req.EndpointID == 0 {
			req.EndpointID = nil
		}
		if req.EndpointVersionID != nil && *req.EndpointVersionID == 0 {
			req.EndpointVersionID = nil
		}

		runID := generateRunID()
		req.RunID = runID

		var endpointVersionID *int64
		var endpointID *int64
		var benchmarkRunId int64

		benchmarkRunId, err := db.WithTx(store, func(tx *sql.Tx) (int64, error) {
			// Check if endpoint should be created or if exists
			if req.EndpointID == nil {
				id, err := InsertEndpointTx(
					tx,
					req.Name,
					req.Method,
					req.URL,
					req.Headers,
					req.Params,
					req.Body,
					req.UserID,
				)
				if err != nil {
					return 0, err
				}
				endpointID = &id
			} else {
				endpointID = req.EndpointID
			}

			if req.ChangesMade || req.EndpointVersionID == nil {
				vid, err := InsertEndpointVersionTx(
					tx,
					*endpointID,
					1,
					req.Method,
					req.Headers,
					req.Params,
					req.Body,
					req.URL,
				)
				if err != nil {
					return 0, err
				}
				endpointVersionID = &vid
			} else {
				endpointVersionID = req.EndpointVersionID
			}

			return store.InsertBenchmarkRunTx(tx, db.BenchmarkRunInsert{
				EndpointVersionID: endpointVersionID,
				Concurrency:       req.Concurrency,
				RateLimit:         req.RateLimit,
				DurationSeconds:   req.DurationSec,
				UserID:            req.UserID,
			})
		})
		if err != nil {
			log.Printf("insert benchmark run failed: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		ctx, cancel := context.WithCancel(context.Background())

		run := &BenchmarkRun{
			ID:      int(benchmarkRunId),
			Request: req,
			// MaxSuccess: int64(req.Concurrency),
			Status:    StatusPending,
			StartedAt: time.Now(),
			ctx:       ctx,
			cancel:    cancel,
			Metrics:   &BenchmarkMetrics{},
		}

		runsMu.Lock()
		runs[runID] = run
		runsMu.Unlock()

		go runBenchmarkAsync(store, run)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"run_id": runID,
		})
	}
}

func runBenchmarkAsync(store *db.DB, run *BenchmarkRun) {
	runsMu.Lock()
	run.Status = StatusRunning
	if err := store.MarkBenchmarkRunRunning(run.ID); err != nil {
		log.Printf("failed to mark run running: %v", err)
	}
	runsMu.Unlock()

	if run.Status != StatusRunning {
		log.Printf("run status not running")
		return
	}
	const maxConsecutiveErrors = 3
	errorTracker := NewErrorTracker(maxConsecutiveErrors, run.cancel, run.Metrics)

	ctx := run.ctx

	timer := time.NewTimer(time.Duration(run.Request.DurationSec) * time.Second)
	defer timer.Stop()

	var wg sync.WaitGroup

	if run.Request.CacheMode == CacheWarm {
		log.Printf("warming cache for %s", run.ID)
		warmCache(run.Request)
	}

	run.Metrics.addLog("info", fmt.Sprintf("benchmark started: %d workers, %ds duration", run.Request.Concurrency, run.Request.DurationSec))

	limiter := newRateLimiter(run.Request.RateLimit)
	for i := 0; i < run.Request.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			benchmarkWorker(ctx, workerID, run.Request, run.Metrics, errorTracker, limiter)
		}(i)
	}

	select {
	case <-timer.C:
		run.StopReason = StopCompleted
	case <-run.ctx.Done():
		run.StopReason = StopErrors
	}

	run.cancel()
	wg.Wait()

	run.Metrics.addLog("info", fmt.Sprintf("benchmark finished: %s", run.StopReason))

	end := time.Now()

	metrics := run.Metrics
	runsMu.Lock()
	run.Status = StatusCompleted
	latencies := append([]time.Duration(nil), metrics.Latencies...)
	responseSizes := append([]int64(nil), metrics.ResponseSizes...)
	run.EndedAt = &end
	runsMu.Unlock()

	result := &BenchmarkResult{
		Requests:  metrics.RequestsTotal,
		Errors:    metrics.ErrorsTotal,
		P50Ms:     percentile(latencies, 50).Milliseconds(),
		P95Ms:     percentile(latencies, 95).Milliseconds(),
		P99Ms:     percentile(latencies, 99).Milliseconds(),
		Status2xx: metrics.Status2xx,
		Status3xx: metrics.Status3xx,
		Status4xx: metrics.Status4xx,
		Status5xx: metrics.Status5xx,
	}

	// Compute min/max latency
	if len(latencies) > 0 {
		// latencies are already sorted by percentile() calls above
		result.MinMs = float64(latencies[0].Milliseconds())
		result.MaxMs = float64(latencies[len(latencies)-1].Milliseconds())

		var totalMs float64
		for _, l := range latencies {
			totalMs += float64(l.Milliseconds())
		}
		result.AvgMs = totalMs / float64(len(latencies))
	}

	// Compute response size stats
	if len(responseSizes) > 0 {
		minBytes := responseSizes[0]
		maxBytes := responseSizes[0]
		var totalBytes int64
		for _, s := range responseSizes {
			totalBytes += s
			if s < minBytes {
				minBytes = s
			}
			if s > maxBytes {
				maxBytes = s
			}
		}
		result.AvgResponseBytes = totalBytes / int64(len(responseSizes))
		result.MinResponseBytes = minBytes
		result.MaxResponseBytes = maxBytes
	}

	// Finalize run
	_, err := db.WithTx(store, func(tx *sql.Tx) (int64, error) {
		if err := store.FinalizeBenchmarkRun(
			tx,
			run.ID,
			"completed",
			string(run.StopReason),
		); err != nil {
			return 0, err
		}

		return store.InsertBenchmarkMetrics(tx, db.BenchmarkMetricsInsert{
			BenchmarkRunID:   run.ID,
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
		})
	})

	if err != nil {
		log.Printf("failed to persist benchmark result: %v", err)
		run.Metrics.addLog("error", "failed to persist benchmark results to database")
	}

	runsMu.Lock()
	run.Status = StatusCompleted
	run.EndedAt = &end

	// Set cache metrics
	result.Cache.Hits = metrics.CacheHits
	result.Cache.Misses = metrics.CacheMisses

	if len(metrics.HitLat) > 0 {
		result.Cache.HitP95Ms = percentile(metrics.HitLat, 95).Milliseconds()
	}
	if len(metrics.MissLat) > 0 {
		result.Cache.MissP95Ms = percentile(metrics.MissLat, 95).Milliseconds()
	}

	run.Result = result
	runsMu.Unlock()

}

func stopBenchmarkHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	runsMu.RLock()
	run, ok := runs[id]
	runsMu.RUnlock()

	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	run.cancel()

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "stopping",
	})
}

func getBenchmarkStatusHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	runsMu.RLock()
	run, ok := runs[id]
	runsMu.RUnlock()

	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(run)
	// resp := BenchmarkStatusResponse{
	// 	ID:         run.ID,
	// 	Status:     run.Status,
	// 	StartedAt:  run.StartedAt,
	// 	EndedAt:    run.EndedAt,
	// 	Requests:   atomic.LoadInt64(&metrics.SuccessTotal),
	// 	Errors:     atomic.LoadInt64(&metrics.ErrorsTotal),
	// 	P50Ms:      percentile(latencies, 50).Milliseconds(),
	// 	P95Ms:      percentile(latencies, 95).Milliseconds(),
	// 	StopReason: run.StopReason,
	// }
	// json.NewEncoder(w).Encode(resp)
}
