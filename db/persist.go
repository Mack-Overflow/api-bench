package db

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
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

	Concurrency     int
	RateLimit       int
	DurationSeconds int
	ThrottleTimeMs  int

	Status     string
	StopReason string

	Metrics BenchmarkMetricsInsert

	// EndpointVersionID, if non-nil, links the run to this exact version and
	// skips endpoint/version upsert. Used when the caller already resolved the
	// version (e.g. `run -e foo -v 3`).
	EndpointVersionID *int64
}

// PersistBenchmarkResult writes a completed benchmark to the database. It
// upserts the endpoint by (user_id, name), creates a new version only if the
// request config differs from the latest stored version, and links the run
// and metrics. Returns the benchmark run ID.
func PersistBenchmarkResult(store *DB, input PersistInput) (int64, error) {
	return WithTx(store, func(tx *sql.Tx) (int64, error) {
		var versionID int64

		if input.EndpointVersionID != nil {
			versionID = *input.EndpointVersionID
		} else {
			vID, err := EnsureEndpointAndVersionTx(tx, input.UserID, input.Name,
				input.Method, input.URL, input.Headers, input.Params, input.Body)
			if err != nil {
				return 0, err
			}
			versionID = vID
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

// EnsureEndpointAndVersionTx finds-or-creates an endpoint with the given name
// for the user, then either reuses the latest version (if its config matches)
// or inserts a new version with version = max(version)+1. Returns the
// resolved endpoint_versions.id.
func EnsureEndpointAndVersionTx(
	tx *sql.Tx,
	userID *int64,
	name, method, url string,
	headers, params, body []byte,
) (int64, error) {
	endpointID, err := findOrInsertEndpointTx(tx, userID, name, method, url, headers, params, body)
	if err != nil {
		return 0, fmt.Errorf("upsert endpoint: %w", err)
	}

	latest, err := getLatestEndpointVersionTx(tx, endpointID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("lookup latest version: %w", err)
	}

	if latest != nil && versionMatchesConfig(latest, method, url, headers, params, body) {
		return latest.ID, nil
	}

	nextVersion := 1
	if latest != nil {
		nextVersion = latest.Version + 1
	}

	versionID, err := InsertEndpointVersionTx(tx, endpointID, int64(nextVersion), method, headers, params, body, url)
	if err != nil {
		return 0, fmt.Errorf("insert endpoint version: %w", err)
	}
	return versionID, nil
}

func findOrInsertEndpointTx(
	tx *sql.Tx,
	userID *int64,
	name, method, url string,
	headers, params, body []byte,
) (int64, error) {
	var (
		row *sql.Row
		id  int64
	)
	if userID == nil {
		row = tx.QueryRow(`
			SELECT id FROM endpoints
			WHERE user_id IS NULL AND name = $1
			ORDER BY id DESC LIMIT 1
		`, name)
	} else {
		row = tx.QueryRow(`
			SELECT id FROM endpoints
			WHERE user_id = $1 AND name = $2
			ORDER BY id DESC LIMIT 1
		`, *userID, name)
	}
	if err := row.Scan(&id); err == nil {
		return id, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}

	return InsertEndpointTx(tx, name, method, url, headers, params, body, userID)
}

func getLatestEndpointVersionTx(tx *sql.Tx, endpointID int64) (*EndpointVersion, error) {
	row := tx.QueryRow(`
		SELECT id, endpoint_id, version, method, url, headers, params, body, created_at
		FROM endpoint_versions
		WHERE endpoint_id = $1
		ORDER BY version DESC
		LIMIT 1
	`, endpointID)

	var v EndpointVersion
	if err := row.Scan(&v.ID, &v.EndpointID, &v.Version, &v.Method, &v.URL, &v.Headers, &v.Params, &v.Body, &v.CreatedAt); err != nil {
		return nil, err
	}
	return &v, nil
}

func versionMatchesConfig(v *EndpointVersion, method, url string, headers, params, body []byte) bool {
	if v.Method != method || v.URL != url {
		return false
	}
	return jsonEqual(v.Headers, headers) && jsonEqual(v.Params, params) && jsonEqual(v.Body, body)
}

// jsonEqual compares two JSON byte slices semantically. Empty/nil values are
// treated as equivalent. Falls back to byte equality if either side is not
// valid JSON.
func jsonEqual(a, b []byte) bool {
	aEmpty := len(a) == 0 || string(a) == "null"
	bEmpty := len(b) == 0 || string(b) == "null"
	if aEmpty && bEmpty {
		return true
	}
	if aEmpty != bEmpty {
		return false
	}

	var av, bv interface{}
	if err := json.Unmarshal(a, &av); err != nil {
		return bytes.Equal(a, b)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return bytes.Equal(a, b)
	}

	aBytes, _ := json.Marshal(av)
	bBytes, _ := json.Marshal(bv)
	return bytes.Equal(aBytes, bBytes)
}
