package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

type BenchmarkStatus string

const (
	StatusPending   BenchmarkStatus = "pending"
	StatusRunning   BenchmarkStatus = "running"
	StatusCompleted BenchmarkStatus = "completed"
	StatusFailed    BenchmarkStatus = "failed"
)

type StartBenchmarkRequest struct {
	URL         string            `json:"url"`
	Method      string            `json:"method"`
	Headers     map[string]string `json:"headers"`
	Params      map[string]string `json:"params"`
	Body        any               `json:"body,omitempty"`
	Concurrency int               `json:"concurrency"`
	RateLimit   int               `json:"rate_limit"`
	DurationSec int               `json:"duration_seconds"`
}

type StopReason string

const (
	StopCompleted StopReason = "completed"
	StopCanceled  StopReason = "canceled"
	StopErrors    StopReason = "consecutive_errors"
)

type BenchmarkRun struct {
	ID        string
	Request   StartBenchmarkRequest
	Status    BenchmarkStatus
	StartedAt time.Time
	EndedAt   *time.Time

	ctx    context.Context
	cancel context.CancelFunc

	Metrics    *BenchmarkMetrics
	MaxSuccess int64
	Result     *BenchmarkResult
	StopReason StopReason `json:"stop_reason,omitempty"`
}

type BenchmarkResult struct {
	Requests int     `json:"requests"`
	Errors   int     `json:"errors_total"`
	AvgMs    float64 `json:"avg_ms"`
	P50Ms    int64   `json:"p50_ms"`
	P95Ms    int64   `json:"p95_ms"`
}

var (
	runs   = make(map[string]*BenchmarkRun)
	runsMu sync.RWMutex
)

func generateRunID() string {
	return fmt.Sprintf("run_%d", time.Now().UnixNano())
}

func validateStartRequest(req *StartBenchmarkRequest) error {
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

func startBenchmarkHandler(w http.ResponseWriter, r *http.Request) {
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

	runID := generateRunID()

	ctx, cancel := context.WithCancel(context.Background())

	run := &BenchmarkRun{
		ID:         runID,
		Request:    req,
		MaxSuccess: int64(req.Concurrency),
		Status:     StatusPending,
		StartedAt:  time.Now(),
		ctx:        ctx,
		cancel:     cancel,
		Metrics:    &BenchmarkMetrics{},
	}

	runsMu.Lock()
	runs[runID] = run
	runsMu.Unlock()

	go runBenchmarkAsync(run)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"run_id": runID,
	})
}

func runBenchmarkAsync(run *BenchmarkRun) {
	runsMu.Lock()
	run.Status = StatusRunning
	runsMu.Unlock()

	if run.Status != StatusRunning {
		log.Printf("run status not running")
		return
	}
	const maxConsecutiveErrors = 3
	errorTracker := NewErrorTracker(maxConsecutiveErrors, run.cancel)

	ctx := run.ctx

	timer := time.NewTimer(time.Duration(run.Request.DurationSec) * time.Second)
	defer timer.Stop()

	var wg sync.WaitGroup

	limiter := newRateLimiter(run.Request.RateLimit)
	for i := 0; i < run.Request.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			benchmarkWorker(ctx, workerID, run.Request, run.Metrics, run.MaxSuccess, errorTracker, limiter)
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

	end := time.Now()

	metrics := run.Metrics
	runsMu.Lock()
	run.Status = StatusCompleted
	latencies := append([]time.Duration(nil), metrics.Latencies...)
	run.EndedAt = &end
	runsMu.Unlock()
	result := &BenchmarkResult{
		Requests: metrics.RequestsTotal,
		Errors:   metrics.ErrorsTotal,
		P50Ms:    percentile(latencies, 50).Milliseconds(),
		P95Ms:    percentile(latencies, 95).Milliseconds(),
	}

	runsMu.Lock()
	run.Status = StatusCompleted
	run.EndedAt = &end
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
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/benchmarks/start", startBenchmarkHandler)
	mux.HandleFunc("/benchmarks/status", getBenchmarkStatusHandler)
	mux.HandleFunc("/benchmarks/stream", benchmarkStreamHandler)
	mux.HandleFunc("/benchmarks/stop", stopBenchmarkHandler)

	handler := withCORS(mux)

	log.Fatal(http.ListenAndServe(":"+port, handler))
}
