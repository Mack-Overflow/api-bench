package main

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

var benchmarkLog *log.Logger

func init() {
	f, err := os.OpenFile("tmp/benchmark.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("failed to open benchmark log file: %v", err)
		benchmarkLog = log.New(os.Stdout, "[benchmark] ", log.LstdFlags)
		return
	}
	benchmarkLog = log.New(f, "", log.LstdFlags)
}

type Metrics struct {
	Count      int64
	ErrorCount int64
	Latencies  []int64
}

func aggregate(results <-chan Result, metrics *Metrics) {
	for r := range results {
		metrics.Count++
		if r.Error {
			metrics.ErrorCount++
			continue
		}
		metrics.Latencies = append(metrics.Latencies, r.Latency.Milliseconds())
	}
}

type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
}

type BenchmarkMetrics struct {
	mu sync.Mutex

	RequestsTotal int   `json:"requests_total"`
	SuccessTotal  int64 `json:"success_total"`
	ErrorsTotal   int   `json:"errors_total"`
	Latencies     []time.Duration
	AvgLatencyMs  float64 `json:"avg_latency_ms"`

	// Response sizes (bytes)
	ResponseSizes []int64

	// Status code distribution
	Status2xx int `json:"status_2xx"`
	Status3xx int `json:"status_3xx"`
	Status4xx int `json:"status_4xx"`
	Status5xx int `json:"status_5xx"`

	HitLat  []time.Duration
	MissLat []time.Duration

	CacheHits   int
	CacheMisses int

	Logs []LogEntry
}

type MetricsSnapshot struct {
	Requests int        `json:"requests"`
	Errors   int        `json:"errors"`
	P50Ms    int64      `json:"p50_ms,omitempty"`
	P95Ms    int64      `json:"p95_ms,omitempty"`
	Logs     []LogEntry `json:"logs,omitempty"`
}

func (m *BenchmarkMetrics) addLog(level, msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Logs = append(m.Logs, LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Message:   msg,
	})
	benchmarkLog.Println(fmt.Sprintf("[%s] %s", level, msg))
}

// SnapshotLogs returns metrics and any logs added since the given cursor.
// It returns the new cursor value for the next call.
func (m *BenchmarkMetrics) SnapshotLogs(logCursor int) (MetricsSnapshot, int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	latencies := append([]time.Duration(nil), m.Latencies...)

	snap := MetricsSnapshot{
		Requests: m.RequestsTotal,
		Errors:   m.ErrorsTotal,
	}

	if len(latencies) > 0 {
		snap.P50Ms = percentile(latencies, 50).Milliseconds()
		snap.P95Ms = percentile(latencies, 95).Milliseconds()
	}

	if logCursor < len(m.Logs) {
		snap.Logs = append([]LogEntry(nil), m.Logs[logCursor:]...)
		logCursor = len(m.Logs)
	}

	return snap, logCursor
}

func (m *BenchmarkMetrics) record(latency time.Duration, err error, statusCode int, responseBytes int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	log.Printf(
		"RECORD: req=%d err=%v lat=%s status=%d bytes=%d",
		m.RequestsTotal,
		err != nil,
		latency,
		statusCode,
		responseBytes,
	)

	m.RequestsTotal++

	// Track status distribution
	switch {
	case statusCode >= 200 && statusCode < 300:
		m.Status2xx++
	case statusCode >= 300 && statusCode < 400:
		m.Status3xx++
	case statusCode >= 400 && statusCode < 500:
		m.Status4xx++
	case statusCode >= 500:
		m.Status5xx++
	}

	if err != nil {
		m.ErrorsTotal++
		return
	}

	m.Latencies = append(m.Latencies, latency)

	if responseBytes > 0 {
		m.ResponseSizes = append(m.ResponseSizes, responseBytes)
	}
}

func (m *BenchmarkMetrics) recordWithCache(
	lat time.Duration,
	err error,
	cacheHit *bool,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.RequestsTotal++
	if err != nil {
		m.ErrorsTotal++
		return
	}

	m.Latencies = append(m.Latencies, lat)

	if cacheHit != nil {
		if *cacheHit {
			m.CacheHits++
			m.HitLat = append(m.HitLat, lat)
		} else {
			m.CacheMisses++
			m.MissLat = append(m.MissLat, lat)
		}
	}
}
