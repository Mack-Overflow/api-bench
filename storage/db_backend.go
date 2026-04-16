package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Mack-Overflow/api-bench/benchmark"
	"github.com/Mack-Overflow/api-bench/config"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// dbBackend implements StorageBackend for SQL databases (Postgres and MySQL).
// It uses the existing Laravel-managed schema (endpoints, endpoint_versions,
// benchmark_runs, benchmark_metrics) with dialect-specific SQL.
type dbBackend struct {
	db           *sql.DB
	ph           func(int) string // placeholder generator, 1-indexed: "$1" or "?"
	useReturning bool             // true for Postgres (RETURNING id), false for MySQL (LastInsertId)
	jsonCast     string           // "::json" for Postgres, "" for MySQL
}

// Close releases the underlying database connection pool.
func (b *dbBackend) Close() error {
	return b.db.Close()
}

// --- Constructors ---

// NewPostgresBackend creates a StorageBackend connected to PostgreSQL using
// the settings in the config file. The password is resolved from the named
// environment variable at runtime.
func NewPostgresBackend(cfg config.PostgresDriverConfig) (*dbBackend, error) {
	password := os.Getenv(cfg.PasswordEnv)
	if password == "" {
		return nil, fmt.Errorf("password env var $%s is not set", cfg.PasswordEnv)
	}

	sslMode := "disable"
	if cfg.SSL {
		sslMode = "require"
	}

	u := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, password),
		Host:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Path:     cfg.Database,
		RawQuery: "sslmode=" + sslMode,
	}

	db, err := sql.Open("pgx", u.String())
	if err != nil {
		return nil, fmt.Errorf("opening postgres connection: %w", err)
	}
	configurePool(db)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connecting to postgres at %s:%d: %w", cfg.Host, cfg.Port, err)
	}

	return &dbBackend{
		db:           db,
		ph:           func(n int) string { return fmt.Sprintf("$%d", n) },
		useReturning: true,
		jsonCast:     "::json",
	}, nil
}

// NewMySQLBackend creates a StorageBackend connected to MySQL using the
// settings in the config file.
func NewMySQLBackend(cfg config.MySQLDriverConfig) (*dbBackend, error) {
	password := os.Getenv(cfg.PasswordEnv)
	if password == "" {
		return nil, fmt.Errorf("password env var $%s is not set", cfg.PasswordEnv)
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true",
		cfg.User, password, cfg.Host, cfg.Port, cfg.Database)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening mysql connection: %w", err)
	}
	configurePool(db)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connecting to mysql at %s:%d: %w", cfg.Host, cfg.Port, err)
	}

	return &dbBackend{
		db:           db,
		ph:           func(n int) string { return "?" },
		useReturning: false,
		jsonCast:     "",
	}, nil
}

func configurePool(db *sql.DB) {
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)
}

// TestConnection opens a connection, pings, and closes. Used by `config test`.
func TestConnection(cfg *config.Config) error {
	switch cfg.Storage.Local.Driver {
	case "postgres":
		b, err := NewPostgresBackend(cfg.Storage.Local.Postgres)
		if err != nil {
			return err
		}
		b.Close()
		return nil
	case "mysql":
		b, err := NewMySQLBackend(cfg.Storage.Local.MySQL)
		if err != nil {
			return err
		}
		b.Close()
		return nil
	default:
		return fmt.Errorf("TestConnection not applicable for driver %q", cfg.Storage.Local.Driver)
	}
}

// --- StorageBackend: SaveRun ---

func (b *dbBackend) SaveRun(ctx context.Context, run BenchmarkRun) error {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	endpointID, err := b.insertEndpoint(ctx, tx, run)
	if err != nil {
		return fmt.Errorf("insert endpoint: %w", err)
	}

	versionID, err := b.insertEndpointVersion(ctx, tx, endpointID, run)
	if err != nil {
		return fmt.Errorf("insert endpoint version: %w", err)
	}

	runID, err := b.insertBenchmarkRun(ctx, tx, versionID, run)
	if err != nil {
		return fmt.Errorf("insert benchmark run: %w", err)
	}

	if err := b.insertBenchmarkMetrics(ctx, tx, runID, run.Result); err != nil {
		return fmt.Errorf("insert benchmark metrics: %w", err)
	}

	return tx.Commit()
}

func (b *dbBackend) insertEndpoint(ctx context.Context, tx *sql.Tx, run BenchmarkRun) (int64, error) {
	jc := b.jsonCast
	query := fmt.Sprintf(
		`INSERT INTO endpoints (name, method, url, headers, params, body, created_at, updated_at)
		 VALUES (%s, %s, %s, %s%s, %s%s, %s%s, %s, %s)`,
		b.ph(1), b.ph(2), b.ph(3),
		b.ph(4), jc, b.ph(5), jc, b.ph(6), jc,
		b.ph(7), b.ph(8))

	now := time.Now()
	args := []any{
		run.Name, run.Method, run.URL,
		jsonBytes(run.Headers), jsonBytes(run.Params), jsonBytes(run.Body),
		now, now,
	}
	return b.insertAndGetID(ctx, tx, query, args...)
}

func (b *dbBackend) insertEndpointVersion(ctx context.Context, tx *sql.Tx, endpointID int64, run BenchmarkRun) (int64, error) {
	jc := b.jsonCast
	query := fmt.Sprintf(
		`INSERT INTO endpoint_versions (endpoint_id, version, method, url, headers, params, body, created_at, updated_at)
		 VALUES (%s, 1, %s, %s, %s%s, %s%s, %s%s, %s, %s)`,
		b.ph(1), b.ph(2), b.ph(3),
		b.ph(4), jc, b.ph(5), jc, b.ph(6), jc,
		b.ph(7), b.ph(8))

	now := time.Now()
	args := []any{
		endpointID, run.Method, run.URL,
		jsonBytes(run.Headers), jsonBytes(run.Params), jsonBytes(run.Body),
		now, now,
	}
	return b.insertAndGetID(ctx, tx, query, args...)
}

func (b *dbBackend) insertBenchmarkRun(ctx context.Context, tx *sql.Tx, versionID int64, run BenchmarkRun) (int64, error) {
	query := fmt.Sprintf(
		`INSERT INTO benchmark_runs
		 (endpoint_version_id, status, concurrency, rate_limit, duration_seconds,
		  throttle_time_ms, stop_reason, started_at, ended_at, created_at, updated_at)
		 VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)`,
		b.ph(1), b.ph(2), b.ph(3), b.ph(4), b.ph(5),
		b.ph(6), b.ph(7), b.ph(8), b.ph(9), b.ph(10), b.ph(11))

	now := time.Now()
	args := []any{
		versionID, "completed", run.Concurrency, run.RateLimit, run.DurationSec,
		run.ThrottleTimeMs, run.StopReason, run.StartedAt, run.EndedAt, now, now,
	}
	return b.insertAndGetID(ctx, tx, query, args...)
}

func (b *dbBackend) insertBenchmarkMetrics(ctx context.Context, tx *sql.Tx, runID int64, r benchmark.BenchmarkResult) error {
	query := fmt.Sprintf(
		`INSERT INTO benchmark_metrics
		 (benchmark_run_id, requests_total, errors_total, avg_ms, p50_ms, p95_ms, p99_ms,
		  min_ms, max_ms, avg_response_bytes, min_response_bytes, max_response_bytes,
		  status_2xx, status_3xx, status_4xx, status_5xx, created_at, updated_at)
		 VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)`,
		b.ph(1), b.ph(2), b.ph(3), b.ph(4), b.ph(5), b.ph(6), b.ph(7),
		b.ph(8), b.ph(9), b.ph(10), b.ph(11), b.ph(12),
		b.ph(13), b.ph(14), b.ph(15), b.ph(16), b.ph(17), b.ph(18))

	now := time.Now()
	_, err := tx.ExecContext(ctx, query,
		runID, r.Requests, r.Errors, r.AvgMs, r.P50Ms, r.P95Ms, r.P99Ms,
		r.MinMs, r.MaxMs, r.AvgResponseBytes, r.MinResponseBytes, r.MaxResponseBytes,
		r.Status2xx, r.Status3xx, r.Status4xx, r.Status5xx, now, now,
	)
	return err
}

// insertAndGetID executes an INSERT and returns the generated ID.
// Postgres uses RETURNING id; MySQL uses LastInsertId().
func (b *dbBackend) insertAndGetID(ctx context.Context, tx *sql.Tx, query string, args ...any) (int64, error) {
	if b.useReturning {
		query += " RETURNING id"
		var id int64
		err := tx.QueryRowContext(ctx, query, args...).Scan(&id)
		return id, err
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// --- StorageBackend: ListRuns ---

func (b *dbBackend) ListRuns(ctx context.Context, filter RunFilter) ([]RunSummary, error) {
	var conditions []string
	var args []any
	n := 1

	if filter.Endpoint != "" {
		conditions = append(conditions, fmt.Sprintf("e.url LIKE %s", b.ph(n)))
		args = append(args, "%"+filter.Endpoint+"%")
		n++
	}
	if filter.Since != nil {
		conditions = append(conditions, fmt.Sprintf("br.started_at >= %s", b.ph(n)))
		args = append(args, *filter.Since)
		n++
	}
	if filter.Before != nil {
		conditions = append(conditions, fmt.Sprintf("br.started_at < %s", b.ph(n)))
		args = append(args, *filter.Before)
		n++
	}

	query := `SELECT br.id, e.url, e.method, e.name, br.started_at,
	                 bm.requests_total, bm.errors_total, bm.avg_ms, bm.p95_ms,
	                 COALESCE(br.stop_reason, '')
	          FROM benchmark_runs br
	          JOIN endpoint_versions ev ON br.endpoint_version_id = ev.id
	          JOIN endpoints e ON ev.endpoint_id = e.id
	          JOIN benchmark_metrics bm ON bm.benchmark_run_id = br.id`

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY br.started_at DESC"

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list runs query: %w", err)
	}
	defer rows.Close()

	var results []RunSummary
	for rows.Next() {
		var s RunSummary
		var dbID int64
		if err := rows.Scan(
			&dbID, &s.URL, &s.Method, &s.Name, &s.StartedAt,
			&s.Requests, &s.Errors, &s.AvgMs, &s.P95Ms, &s.StopReason,
		); err != nil {
			return nil, fmt.Errorf("scanning run summary: %w", err)
		}
		s.ID = strconv.FormatInt(dbID, 10)
		results = append(results, s)
	}
	return results, rows.Err()
}

// --- StorageBackend: GetRun ---

func (b *dbBackend) GetRun(ctx context.Context, id string) (BenchmarkRun, error) {
	runID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return BenchmarkRun{}, fmt.Errorf("invalid run ID %q (database IDs are numeric)", id)
	}

	query := fmt.Sprintf(`
		SELECT br.id, e.name, e.url, e.method,
		       ev.headers, ev.params, ev.body,
		       br.concurrency, br.duration_seconds,
		       COALESCE(br.rate_limit, 0), br.throttle_time_ms,
		       br.started_at, br.ended_at, COALESCE(br.stop_reason, ''),
		       bm.requests_total, bm.errors_total, bm.avg_ms,
		       bm.p50_ms, bm.p95_ms, COALESCE(bm.p99_ms, 0),
		       COALESCE(bm.min_ms, 0), COALESCE(bm.max_ms, 0),
		       COALESCE(bm.avg_response_bytes, 0),
		       COALESCE(bm.min_response_bytes, 0),
		       COALESCE(bm.max_response_bytes, 0),
		       bm.status_2xx, bm.status_3xx, bm.status_4xx, bm.status_5xx
		FROM benchmark_runs br
		JOIN endpoint_versions ev ON br.endpoint_version_id = ev.id
		JOIN endpoints e ON ev.endpoint_id = e.id
		JOIN benchmark_metrics bm ON bm.benchmark_run_id = br.id
		WHERE br.id = %s`, b.ph(1))

	var run BenchmarkRun
	var dbID int64
	var endedAt sql.NullTime
	var headers, params, body []byte

	err = b.db.QueryRowContext(ctx, query, runID).Scan(
		&dbID, &run.Name, &run.URL, &run.Method,
		&headers, &params, &body,
		&run.Concurrency, &run.DurationSec,
		&run.RateLimit, &run.ThrottleTimeMs,
		&run.StartedAt, &endedAt, &run.StopReason,
		&run.Result.Requests, &run.Result.Errors, &run.Result.AvgMs,
		&run.Result.P50Ms, &run.Result.P95Ms, &run.Result.P99Ms,
		&run.Result.MinMs, &run.Result.MaxMs,
		&run.Result.AvgResponseBytes,
		&run.Result.MinResponseBytes,
		&run.Result.MaxResponseBytes,
		&run.Result.Status2xx, &run.Result.Status3xx,
		&run.Result.Status4xx, &run.Result.Status5xx,
	)
	if err == sql.ErrNoRows {
		return BenchmarkRun{}, fmt.Errorf("run %s not found", id)
	}
	if err != nil {
		return BenchmarkRun{}, fmt.Errorf("get run: %w", err)
	}

	run.ID = strconv.FormatInt(dbID, 10)
	if endedAt.Valid {
		run.EndedAt = endedAt.Time
	}
	run.Headers = headers
	run.Params = params
	run.Body = body

	return run, nil
}

// --- StorageBackend: DeleteRun ---

func (b *dbBackend) DeleteRun(ctx context.Context, id string) error {
	runID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid run ID %q (database IDs are numeric)", id)
	}

	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete child records first
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM benchmark_latency_samples WHERE benchmark_run_id = %s", b.ph(1)), runID); err != nil {
		return fmt.Errorf("delete latency samples: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM benchmark_metrics WHERE benchmark_run_id = %s", b.ph(1)), runID); err != nil {
		return fmt.Errorf("delete metrics: %w", err)
	}

	// Delete the run
	result, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM benchmark_runs WHERE id = %s", b.ph(1)), runID)
	if err != nil {
		return fmt.Errorf("delete run: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("run %s not found", id)
	}

	return tx.Commit()
}

// --- helpers ---

// jsonBytes returns data as []byte, defaulting to "{}" if nil/empty.
func jsonBytes(data json.RawMessage) []byte {
	if data == nil || len(data) == 0 {
		return []byte("{}")
	}
	return []byte(data)
}

// Ensure *dbBackend implements both StorageBackend and io.Closer.
var (
	_ StorageBackend = (*dbBackend)(nil)
	_ io.Closer      = (*dbBackend)(nil)
)
