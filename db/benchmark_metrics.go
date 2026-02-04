package db

import "database/sql"

type BenchmarkMetricsInsert struct {
	BenchmarkRunID int
	Requests       int
	Errors         int
	AvgMs          float64
	P50Ms          int64
	P95Ms          int64
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
			created_at
		) VALUES ($1, $2, $3, $4, $5, $6, NOW())
	`,
		m.BenchmarkRunID,
		m.Requests,
		m.Errors,
		m.AvgMs,
		m.P50Ms,
		m.P95Ms,
	)
	return 0, err
}
