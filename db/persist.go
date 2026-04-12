package db

import (
	"database/sql"
	"fmt"
)

// PersistInput contains everything needed to persist a completed benchmark run.
type PersistInput struct {
	Name    string
	Method  string
	URL     string
	Headers []byte
	Params  []byte
	Body    []byte
	UserID  *int64

	Concurrency    int
	RateLimit      int
	DurationSeconds int
	ThrottleTimeMs int

	Status     string
	StopReason string

	Metrics BenchmarkMetricsInsert
}

// PersistBenchmarkResult writes a complete benchmark result to the database in a
// single transaction: endpoint, endpoint version, benchmark run, and metrics.
// Returns the benchmark run ID.
func PersistBenchmarkResult(store *DB, input PersistInput) (int64, error) {
	return WithTx(store, func(tx *sql.Tx) (int64, error) {
		endpointID, err := InsertEndpointTx(
			tx, input.Name, input.Method, input.URL,
			input.Headers, input.Params, input.Body, input.UserID,
		)
		if err != nil {
			return 0, fmt.Errorf("insert endpoint: %w", err)
		}

		versionID, err := InsertEndpointVersionTx(
			tx, endpointID, 1, input.Method,
			input.Headers, input.Params, input.Body, input.URL,
		)
		if err != nil {
			return 0, fmt.Errorf("insert endpoint version: %w", err)
		}

		runID, err := store.InsertBenchmarkRunTx(tx, BenchmarkRunInsert{
			EndpointVersionID: &versionID,
			Concurrency:       input.Concurrency,
			RateLimit:         input.RateLimit,
			DurationSeconds:   input.DurationSeconds,
			ThrottleTimeMs:    input.ThrottleTimeMs,
			UserID:            input.UserID,
		})
		if err != nil {
			return 0, fmt.Errorf("insert benchmark run: %w", err)
		}

		if err := store.FinalizeBenchmarkRun(tx, int(runID), input.Status, input.StopReason); err != nil {
			return 0, fmt.Errorf("finalize benchmark run: %w", err)
		}

		input.Metrics.BenchmarkRunID = int(runID)
		if _, err := store.InsertBenchmarkMetrics(tx, input.Metrics); err != nil {
			return 0, fmt.Errorf("insert metrics: %w", err)
		}

		return runID, nil
	})
}
