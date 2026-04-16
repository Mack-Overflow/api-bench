package storage

import (
	"context"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mack-Overflow/api-bench/benchmark"
	"github.com/Mack-Overflow/api-bench/config"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Unit tests (no database required)
// ---------------------------------------------------------------------------

func TestPostgresDialectSettings(t *testing.T) {
	b := &dbBackend{
		ph:           func(n int) string { return "$" + strconv.Itoa(n) },
		useReturning: true,
		jsonCast:     "::json",
	}
	if b.ph(1) != "$1" || b.ph(10) != "$10" {
		t.Errorf("postgres placeholder: got %s / %s", b.ph(1), b.ph(10))
	}
	if !b.useReturning {
		t.Error("postgres should use RETURNING")
	}
	if b.jsonCast != "::json" {
		t.Error("postgres should cast json")
	}
}

func TestMySQLDialectSettings(t *testing.T) {
	b := &dbBackend{
		ph:           func(n int) string { return "?" },
		useReturning: false,
		jsonCast:     "",
	}
	if b.ph(1) != "?" || b.ph(5) != "?" {
		t.Errorf("mysql placeholder: got %s / %s", b.ph(1), b.ph(5))
	}
	if b.useReturning {
		t.Error("mysql should not use RETURNING")
	}
}

func TestJsonBytesHelper(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{"nil", nil, "{}"},
		{"empty", []byte{}, "{}"},
		{"valid", []byte(`{"key":"val"}`), `{"key":"val"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(jsonBytes(tt.input))
			if got != tt.want {
				t.Errorf("jsonBytes = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewPostgresBackendMissingPassword(t *testing.T) {
	cfg := config.PostgresDriverConfig{
		Host: "localhost", Port: 5432, Database: "test",
		User: "test", PasswordEnv: "BENCH_TEST_NONEXISTENT_VAR_12345",
	}
	_, err := NewPostgresBackend(cfg)
	if err == nil {
		t.Fatal("expected error when password env var is not set")
	}
	if !strings.Contains(err.Error(), "BENCH_TEST_NONEXISTENT_VAR_12345") {
		t.Errorf("error should mention the env var name, got: %v", err)
	}
}

func TestNewMySQLBackendMissingPassword(t *testing.T) {
	cfg := config.MySQLDriverConfig{
		Host: "localhost", Port: 3306, Database: "test",
		User: "test", PasswordEnv: "BENCH_TEST_NONEXISTENT_VAR_12345",
	}
	_, err := NewMySQLBackend(cfg)
	if err == nil {
		t.Fatal("expected error when password env var is not set")
	}
	if !strings.Contains(err.Error(), "BENCH_TEST_NONEXISTENT_VAR_12345") {
		t.Errorf("error should mention the env var name, got: %v", err)
	}
}

func TestFactoryDBDriversMissingPassword(t *testing.T) {
	for _, driver := range []string{"postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			cfg := &config.Config{
				Storage: config.StorageConfig{
					Mode: "local",
					Local: config.LocalStorageConfig{
						Driver: driver,
						Postgres: config.PostgresDriverConfig{
							Host: "localhost", Port: 5432, Database: "test",
							User: "test", PasswordEnv: "BENCH_TEST_NONEXISTENT_VAR_12345",
						},
						MySQL: config.MySQLDriverConfig{
							Host: "localhost", Port: 3306, Database: "test",
							User: "test", PasswordEnv: "BENCH_TEST_NONEXISTENT_VAR_12345",
						},
					},
				},
			}
			_, err := NewBackendFromConfig(cfg)
			if err == nil {
				t.Fatal("expected error for missing password")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Integration tests — run only when BENCH_TEST_PG_DSN is set to a Postgres
// connection string pointing at a database with Laravel-migrated schema.
//
//   export BENCH_TEST_PG_DSN="postgres://benchmarkr:secret@localhost:5432/benchmarkr?sslmode=disable"
//   go test ./storage/ -v -run Integration
// ---------------------------------------------------------------------------

func openPostgresForTest(t *testing.T) *dbBackend {
	t.Helper()
	dsn := os.Getenv("BENCH_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("skipping: BENCH_TEST_PG_DSN not set")
	}

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("invalid BENCH_TEST_PG_DSN: %v", err)
	}

	password, _ := u.User.Password()
	t.Setenv("_BENCH_TEST_PG_PASS", password)

	port := 5432
	if p := u.Port(); p != "" {
		port, _ = strconv.Atoi(p)
	}

	cfg := config.PostgresDriverConfig{
		Host:        u.Hostname(),
		Port:        port,
		Database:    strings.TrimPrefix(u.Path, "/"),
		User:        u.User.Username(),
		PasswordEnv: "_BENCH_TEST_PG_PASS",
		SSL:         u.Query().Get("sslmode") == "require",
	}

	b, err := NewPostgresBackend(cfg)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

func TestPostgresIntegrationCRUD(t *testing.T) {
	b := openPostgresForTest(t)
	ctx := context.Background()

	run := BenchmarkRun{
		ID:          uuid.New().String(),
		Name:        "integration-crud",
		URL:         "https://integration-crud.test/api",
		Method:      "GET",
		Headers:     []byte(`{"Accept":"application/json"}`),
		Params:      []byte(`{}`),
		Body:        []byte(`{}`),
		Concurrency: 2,
		DurationSec: 5,
		CacheMode:   "default",
		StartedAt:   time.Now().Add(-5 * time.Second),
		EndedAt:     time.Now(),
		StopReason:  "completed",
		Result: benchmark.BenchmarkResult{
			Requests:  200,
			Errors:    1,
			AvgMs:     30.5,
			P50Ms:     25,
			P95Ms:     90,
			P99Ms:     150,
			MinMs:     8,
			MaxMs:     300,
			Status2xx: 199,
			Status5xx: 1,
		},
	}

	// SaveRun
	if err := b.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	// ListRuns — find our run by unique endpoint
	results, err := b.ListRuns(ctx, RunFilter{Endpoint: "integration-crud.test"})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("ListRuns returned no results")
	}

	dbID := results[0].ID

	// GetRun
	got, err := b.GetRun(ctx, dbID)
	if err != nil {
		t.Fatalf("GetRun(%s): %v", dbID, err)
	}
	if got.URL != run.URL {
		t.Errorf("URL: got %s, want %s", got.URL, run.URL)
	}
	if got.Result.Requests != 200 {
		t.Errorf("Requests: got %d, want 200", got.Result.Requests)
	}
	if got.Result.P95Ms != 90 {
		t.Errorf("P95Ms: got %d, want 90", got.Result.P95Ms)
	}

	// DeleteRun
	if err := b.DeleteRun(ctx, dbID); err != nil {
		t.Fatalf("DeleteRun: %v", err)
	}

	// Verify deleted
	_, err = b.GetRun(ctx, dbID)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestPostgresIntegrationListFilters(t *testing.T) {
	b := openPostgresForTest(t)
	ctx := context.Background()

	t1 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	runs := []BenchmarkRun{
		makeDBRun("https://filter-test.example/alpha", t1),
		makeDBRun("https://filter-test.example/beta", t2),
	}
	for _, r := range runs {
		if err := b.SaveRun(ctx, r); err != nil {
			t.Fatalf("SaveRun: %v", err)
		}
	}
	t.Cleanup(func() {
		all, _ := b.ListRuns(ctx, RunFilter{Endpoint: "filter-test.example"})
		for _, s := range all {
			b.DeleteRun(ctx, s.ID)
		}
	})

	// Filter by endpoint substring
	alphas, _ := b.ListRuns(ctx, RunFilter{Endpoint: "/alpha"})
	if len(alphas) < 1 {
		t.Error("expected at least 1 /alpha result")
	}

	// Filter by date range
	since := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	ranged, _ := b.ListRuns(ctx, RunFilter{Endpoint: "filter-test.example", Since: &since})
	if len(ranged) < 1 {
		t.Error("expected at least 1 result since 2026-03-01")
	}

	// Limit
	limited, _ := b.ListRuns(ctx, RunFilter{Endpoint: "filter-test.example", Limit: 1})
	if len(limited) != 1 {
		t.Errorf("expected 1 result with Limit=1, got %d", len(limited))
	}
}

// --- helpers ---

func makeDBRun(endpoint string, startedAt time.Time) BenchmarkRun {
	return BenchmarkRun{
		ID: uuid.New().String(), Name: "db-test", URL: endpoint, Method: "GET",
		Headers: []byte(`{}`), Params: []byte(`{}`), Body: []byte(`{}`),
		Concurrency: 1, DurationSec: 5, CacheMode: "default",
		StartedAt: startedAt, EndedAt: startedAt.Add(5 * time.Second), StopReason: "completed",
		Result: benchmark.BenchmarkResult{
			Requests: 50, AvgMs: 20, P50Ms: 18, P95Ms: 50, P99Ms: 80,
			MinMs: 5, MaxMs: 100, Status2xx: 50,
		},
	}
}
