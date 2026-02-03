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

	"github.com/Mack-Overflow/api-bench/db"

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
	RunID             string `json:"run_id"`
	EndpointID        *int64 `json:"endpoint_id"`
	EndpointVersionID *int64 `json:"endpoint_version_id,omitempty"`
	ChangesMade       bool   `json:"changes_made"`

	Name    string          `json:"name"`
	URL     string          `json:"url"`
	Method  string          `json:"method"`
	Headers json.RawMessage `json:"headers"`
	Params  json.RawMessage `json:"params"`
	Body    json.RawMessage `json:"body"`

	Concurrency int       `json:"concurrency"`
	RateLimit   int       `json:"rate_limit"`
	DurationSec int       `json:"duration_seconds"`
	CacheMode   CacheMode `json:"cache_mode"`
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

	Cache struct {
		Hits      int   `json:"hits"`
		Misses    int   `json:"misses"`
		HitP95Ms  int64 `json:"hit_p95_ms,omitempty"`
		MissP95Ms int64 `json:"miss_p95_ms,omitempty"`
	} `json:"cache"`
}

var (
	runs   = make(map[string]*BenchmarkRun)
	runsMu sync.RWMutex
)

func generateRunID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
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

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	// DB connection setup
	sqlDB, err := openDB()
	if err != nil {
		log.Fatal(err)
	}
	db.DefaultPool(sqlDB)

	store := db.New(sqlDB)

	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/benchmarks/start", startBenchmarkHandler(store))
	mux.HandleFunc("/benchmarks/status", getBenchmarkStatusHandler)
	mux.HandleFunc("/benchmarks/stream", benchmarkStreamHandler)
	mux.HandleFunc("/benchmarks/stop", stopBenchmarkHandler)

	handler := withCORS(mux)

	log.Fatal(http.ListenAndServe(":"+port, handler))
}
