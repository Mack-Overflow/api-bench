package db

import "database/sql"

type BenchmarkRunInsert struct {
	ID                string
	EndpointID        int64
	EndpointVersionID *int64
	Concurrency       int
	RateLimit         int
	DurationSeconds   int
}

func (db *DB) InsertBenchmarkRunTx(
	tx *sql.Tx,
	input BenchmarkRunInsert,
) error {
	_, err := tx.Exec(`
		INSERT INTO benchmark_runs (
			endpoint_version_id,
			concurrency,
			rate_limit,
			duration_seconds,
			status,
			created_at
		) VALUES ($1, $2, $3, $4, 'started', NOW())
	`,
		input.EndpointVersionID,
		input.Concurrency,
		input.RateLimit,
		input.DurationSeconds,
	)
	return err
}

func (db *DB) MarkBenchmarkRunRunning(runID string) error {
	_, err := db.Exec(`
		UPDATE benchmark_runs
		SET status = 'running'
		WHERE id = $1
	`, runID)

	return err
}

func (db *DB) FinalizeBenchmarkRun(
	tx *sql.Tx,
	runID string,
	status string,
	stopReason string,
) error {
	_, err := tx.Exec(`
		UPDATE benchmark_runs
		SET
			status = $2,
			stop_reason = $3,
			ended_at = NOW()
		WHERE id = $1
	`,
		runID,
		status,
		stopReason,
	)
	return err
}
