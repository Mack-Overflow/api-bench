package db

import "database/sql"

type BenchmarkRunInsert struct {
	ID                string
	EndpointID        int64
	EndpointVersionID *int64
	Concurrency       int
	RateLimit         int
	DurationSeconds   int
	UserID            *int64
}

func (db *DB) InsertBenchmarkRunTx(
	tx *sql.Tx,
	input BenchmarkRunInsert,
) (int64, error) {
	var id int64

	err := tx.QueryRow(`
		INSERT INTO benchmark_runs (
			endpoint_version_id,
			concurrency,
			rate_limit,
			duration_seconds,
			user_id,
			status,
			created_at
		) VALUES ($1, $2, $3, $4, $5, 'started', NOW())
		RETURNING id
	`,
		input.EndpointVersionID,
		input.Concurrency,
		input.RateLimit,
		input.DurationSeconds,
		input.UserID,
	).Scan(&id)

	return id, err
}

func (db *DB) MarkBenchmarkRunRunning(runID int) error {
	_, err := db.Exec(`
		UPDATE benchmark_runs
		SET status = 'running'
		WHERE id = $1
	`, runID)

	return err
}

func (db *DB) FinalizeBenchmarkRun(
	tx *sql.Tx,
	runID int,
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
