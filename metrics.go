package main

import (
	"sync"
	"time"
)

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

type BenchmarkMetrics struct {
	mu sync.Mutex

	RequestsTotal int   `json:"requests_total"`
	SuccessTotal  int64 `json:"success_total"`
	ErrorsTotal   int   `json:"errors_total"`
	Latencies     []time.Duration
	AvgLatencyMs  float64 `json:"avg_latency_ms"`

	HitLat  []time.Duration
	MissLat []time.Duration

	CacheHits   int
	CacheMisses int
}

type MetricsSnapshot struct {
	Requests int   `json:"requests"`
	Errors   int   `json:"errors"`
	P50Ms    int64 `json:"p50_ms,omitempty"`
	P95Ms    int64 `json:"p95_ms,omitempty"`
}

func (m *BenchmarkMetrics) Snapshot() MetricsSnapshot {
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

	return snap
}

func (m *BenchmarkMetrics) record(latency time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.RequestsTotal++
	if err != nil {
		m.ErrorsTotal++
		return
	}

	m.Latencies = append(m.Latencies, latency)
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
