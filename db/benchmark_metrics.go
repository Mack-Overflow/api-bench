package db

import "database/sql"

type BenchmarkMetricsInsert struct {
	BenchmarkRunID int
	Requests       int
	Errors         int
	AvgMs          float64
	P50Ms          int64
	P95Ms          int64
	P99Ms          int64
	MinMs          float64
	MaxMs          float64

	AvgResponseBytes int64
	MinResponseBytes int64
	MaxResponseBytes int64

	Status2xx int
	Status3xx int
	Status4xx int
	Status5xx int
}

func (db *DB) InsertBenchmarkMetrics(
	tx *sql.Tx,
	m BenchmarkMetricsInsert,
) (int64, error) {
	_, err := tx.Exec(`
		INSERT INTO benchmark_metrics (
			benchmark_run_id,
			requests_total,
			errors_total,
			avg_ms,
			p50_ms,
			p95_ms,
			p99_ms,
			min_ms,
			max_ms,
			avg_response_bytes,
			min_response_bytes,
			max_response_bytes,
			status_2xx,
			status_3xx,
			status_4xx,
			status_5xx,
			created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, NOW())
	`,
		m.BenchmarkRunID,
		m.Requests,
		m.Errors,
		m.AvgMs,
		m.P50Ms,
		m.P95Ms,
		m.P99Ms,
		m.MinMs,
		m.MaxMs,
		m.AvgResponseBytes,
		m.MinResponseBytes,
		m.MaxResponseBytes,
		m.Status2xx,
		m.Status3xx,
		m.Status4xx,
		m.Status5xx,
	)
	return 0, err
}
