package benchmark

import (
	"sync"
	"time"
)

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

	ResponseSizes []int64

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

func (m *BenchmarkMetrics) AddLog(level, msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Logs = append(m.Logs, LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Message:   msg,
	})
}

// SnapshotLogs returns metrics and any logs added since the given cursor.
func (m *BenchmarkMetrics) SnapshotLogs(logCursor int) (MetricsSnapshot, int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	latencies := append([]time.Duration(nil), m.Latencies...)

	snap := MetricsSnapshot{
		Requests: m.RequestsTotal,
		Errors:   m.ErrorsTotal,
	}

	if len(latencies) > 0 {
		snap.P50Ms = Percentile(latencies, 50).Milliseconds()
		snap.P95Ms = Percentile(latencies, 95).Milliseconds()
	}

	if logCursor < len(m.Logs) {
		snap.Logs = append([]LogEntry(nil), m.Logs[logCursor:]...)
		logCursor = len(m.Logs)
	}

	return snap, logCursor
}

func (m *BenchmarkMetrics) Record(latency time.Duration, err error, statusCode int, responseBytes int64, cacheHit *bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.RequestsTotal++

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

	if cacheHit != nil {
		if *cacheHit {
			m.CacheHits++
			m.HitLat = append(m.HitLat, latency)
		} else {
			m.CacheMisses++
			m.MissLat = append(m.MissLat, latency)
		}
	}
}

// ComputeResult calculates the final BenchmarkResult from collected metrics.
func (m *BenchmarkMetrics) ComputeResult() *BenchmarkResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	latencies := append([]time.Duration(nil), m.Latencies...)
	responseSizes := append([]int64(nil), m.ResponseSizes...)

	result := &BenchmarkResult{
		Requests:  m.RequestsTotal,
		Errors:    m.ErrorsTotal,
		P50Ms:     Percentile(latencies, 50).Milliseconds(),
		P95Ms:     Percentile(latencies, 95).Milliseconds(),
		P99Ms:     Percentile(latencies, 99).Milliseconds(),
		Status2xx: m.Status2xx,
		Status3xx: m.Status3xx,
		Status4xx: m.Status4xx,
		Status5xx: m.Status5xx,
	}

	if len(latencies) > 0 {
		// latencies are sorted by Percentile calls above
		result.MinMs = float64(latencies[0].Milliseconds())
		result.MaxMs = float64(latencies[len(latencies)-1].Milliseconds())

		var totalMs float64
		for _, l := range latencies {
			totalMs += float64(l.Milliseconds())
		}
		result.AvgMs = totalMs / float64(len(latencies))
	}

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

	result.Cache.Hits = m.CacheHits
	result.Cache.Misses = m.CacheMisses
	if len(m.HitLat) > 0 {
		result.Cache.HitP95Ms = Percentile(m.HitLat, 95).Milliseconds()
	}
	if len(m.MissLat) > 0 {
		result.Cache.MissP95Ms = Percentile(m.MissLat, 95).Milliseconds()
	}

	return result
}
