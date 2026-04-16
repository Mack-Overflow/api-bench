package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Mack-Overflow/api-bench/benchmark"
	"github.com/Mack-Overflow/api-bench/config"
	"github.com/google/uuid"
)

func makeRun(url string, startedAt time.Time) BenchmarkRun {
	return BenchmarkRun{
		ID:          uuid.New().String(),
		Name:        "test run",
		URL:         url,
		Method:      "GET",
		Concurrency: 1,
		DurationSec: 5,
		CacheMode:   "default",
		StartedAt:   startedAt,
		EndedAt:     startedAt.Add(5 * time.Second),
		StopReason:  "completed",
		Result: benchmark.BenchmarkResult{
			Requests:  100,
			Errors:    2,
			AvgMs:     25.5,
			P50Ms:     20,
			P95Ms:     80,
			P99Ms:     120,
			MinMs:     5,
			MaxMs:     200,
			Status2xx: 98,
			Status5xx: 2,
		},
	}
}

// ---------------------------------------------------------------------------
// SaveRun → file exists on disk with correct contents
// ---------------------------------------------------------------------------

func TestSaveRunCreatesFile(t *testing.T) {
	dir := t.TempDir()
	backend := NewJSONBackend(dir)
	ctx := context.Background()

	run := makeRun("https://api.example.com/v1/posts", time.Now())

	if err := backend.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	// Verify the file exists
	matches, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	// Filter out index.json
	var runFiles []string
	for _, m := range matches {
		if filepath.Base(m) != "index.json" {
			runFiles = append(runFiles, m)
		}
	}
	if len(runFiles) != 1 {
		t.Fatalf("expected 1 run file, got %d: %v", len(runFiles), runFiles)
	}

	// Verify contents
	data, err := os.ReadFile(runFiles[0])
	if err != nil {
		t.Fatalf("reading run file: %v", err)
	}
	var saved BenchmarkRun
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("parsing run file: %v", err)
	}
	if saved.ID != run.ID {
		t.Errorf("ID mismatch: got %s, want %s", saved.ID, run.ID)
	}
	if saved.URL != run.URL {
		t.Errorf("URL mismatch: got %s, want %s", saved.URL, run.URL)
	}
	if saved.Result.Requests != 100 {
		t.Errorf("Requests mismatch: got %d, want 100", saved.Result.Requests)
	}
	if saved.Result.AvgMs != 25.5 {
		t.Errorf("AvgMs mismatch: got %f, want 25.5", saved.Result.AvgMs)
	}
}

func TestSaveRunCreatesOutputDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "deep", "runs")
	backend := NewJSONBackend(dir)

	run := makeRun("https://example.com", time.Now())
	if err := backend.SaveRun(context.Background(), run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("output dir not created: %v", err)
	}
}

func TestSaveRunUpdatesIndex(t *testing.T) {
	dir := t.TempDir()
	backend := NewJSONBackend(dir)
	ctx := context.Background()

	run := makeRun("https://example.com/test", time.Now())
	if err := backend.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun 1: %v", err)
	}
	if err := backend.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun 2: %v", err)
	}

	indexPath := filepath.Join(dir, "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("reading index: %v", err)
	}
	var entries []indexEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("parsing index: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 index entries after two saves, got %d", len(entries))
	}
	if entries[0].ID != run.ID {
		t.Errorf("index ID mismatch")
	}
}

// ---------------------------------------------------------------------------
// GetRun
// ---------------------------------------------------------------------------

func TestGetRun(t *testing.T) {
	dir := t.TempDir()
	backend := NewJSONBackend(dir)
	ctx := context.Background()

	run := makeRun("https://api.example.com/users", time.Now())
	backend.SaveRun(ctx, run)

	got, err := backend.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.ID != run.ID {
		t.Errorf("ID: got %s, want %s", got.ID, run.ID)
	}
	if got.URL != run.URL {
		t.Errorf("URL: got %s, want %s", got.URL, run.URL)
	}
	if got.Result.P95Ms != run.Result.P95Ms {
		t.Errorf("P95Ms: got %d, want %d", got.Result.P95Ms, run.Result.P95Ms)
	}
}

func TestGetRunNotFound(t *testing.T) {
	dir := t.TempDir()
	backend := NewJSONBackend(dir)

	_, err := backend.GetRun(context.Background(), "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent run")
	}
}

func TestGetRunFallbackToGlob(t *testing.T) {
	dir := t.TempDir()
	backend := NewJSONBackend(dir)
	ctx := context.Background()

	run := makeRun("https://example.com/test", time.Now())
	backend.SaveRun(ctx, run)

	// Delete the index to force glob fallback
	os.Remove(filepath.Join(dir, "index.json"))

	got, err := backend.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun with glob fallback: %v", err)
	}
	if got.ID != run.ID {
		t.Errorf("ID mismatch after glob fallback")
	}
}

// ---------------------------------------------------------------------------
// DeleteRun
// ---------------------------------------------------------------------------

func TestDeleteRun(t *testing.T) {
	dir := t.TempDir()
	backend := NewJSONBackend(dir)
	ctx := context.Background()

	run := makeRun("https://example.com/delete-me", time.Now())
	backend.SaveRun(ctx, run)

	if err := backend.DeleteRun(ctx, run.ID); err != nil {
		t.Fatalf("DeleteRun: %v", err)
	}

	// File should be gone
	matches, _ := filepath.Glob(filepath.Join(dir, "*_"+run.ID[:8]+".json"))
	if len(matches) != 0 {
		t.Errorf("run file still exists after delete")
	}

	// Index should be empty
	entries, _ := backend.readIndex()
	if len(entries) != 0 {
		t.Errorf("index still has %d entries after delete", len(entries))
	}
}

func TestDeleteRunNotFound(t *testing.T) {
	dir := t.TempDir()
	backend := NewJSONBackend(dir)

	err := backend.DeleteRun(context.Background(), "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent run")
	}
}

// ---------------------------------------------------------------------------
// ListRuns with filters
// ---------------------------------------------------------------------------

func TestListRunsNoFilter(t *testing.T) {
	dir := t.TempDir()
	backend := NewJSONBackend(dir)
	ctx := context.Background()

	now := time.Now()
	for i := 0; i < 5; i++ {
		run := makeRun(fmt.Sprintf("https://example.com/endpoint-%d", i), now.Add(time.Duration(i)*time.Minute))
		backend.SaveRun(ctx, run)
	}

	results, err := backend.ListRuns(ctx, RunFilter{})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}
	// Should be newest first
	if results[0].StartedAt.Before(results[4].StartedAt) {
		t.Error("results not sorted newest-first")
	}
}

func TestListRunsFilterByEndpoint(t *testing.T) {
	dir := t.TempDir()
	backend := NewJSONBackend(dir)
	ctx := context.Background()

	now := time.Now()
	backend.SaveRun(ctx, makeRun("https://api.example.com/users", now))
	backend.SaveRun(ctx, makeRun("https://api.example.com/posts", now.Add(time.Minute)))
	backend.SaveRun(ctx, makeRun("https://api.example.com/users/123", now.Add(2*time.Minute)))

	results, err := backend.ListRuns(ctx, RunFilter{Endpoint: "/users"})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results matching /users, got %d", len(results))
	}
}

func TestListRunsFilterByDateRange(t *testing.T) {
	dir := t.TempDir()
	backend := NewJSONBackend(dir)
	ctx := context.Background()

	t1 := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)

	backend.SaveRun(ctx, makeRun("https://example.com/a", t1))
	backend.SaveRun(ctx, makeRun("https://example.com/b", t2))
	backend.SaveRun(ctx, makeRun("https://example.com/c", t3))

	since := time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC)

	results, err := backend.ListRuns(ctx, RunFilter{Since: &since, Before: &before})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result in date range, got %d", len(results))
	}
	if results[0].URL != "https://example.com/b" {
		t.Errorf("wrong run in date range: %s", results[0].URL)
	}
}

func TestListRunsLimit(t *testing.T) {
	dir := t.TempDir()
	backend := NewJSONBackend(dir)
	ctx := context.Background()

	now := time.Now()
	for i := 0; i < 10; i++ {
		backend.SaveRun(ctx, makeRun("https://example.com/test", now.Add(time.Duration(i)*time.Minute)))
	}

	results, err := backend.ListRuns(ctx, RunFilter{Limit: 3})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results with Limit=3, got %d", len(results))
	}
}

func TestListRunsEmpty(t *testing.T) {
	dir := t.TempDir()
	backend := NewJSONBackend(dir)

	results, err := backend.ListRuns(context.Background(), RunFilter{})
	if err != nil {
		t.Fatalf("ListRuns on empty dir: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// Concurrent SaveRun calls don't corrupt the index
// ---------------------------------------------------------------------------

func TestConcurrentSaveRun(t *testing.T) {
	dir := t.TempDir()
	backend := NewJSONBackend(dir)
	ctx := context.Background()

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			run := makeRun(fmt.Sprintf("https://example.com/concurrent-%d", i), time.Now().Add(time.Duration(i)*time.Second))
			if err := backend.SaveRun(ctx, run); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent SaveRun error: %v", err)
	}

	// Index should have all entries
	entries, err := backend.readIndex()
	if err != nil {
		t.Fatalf("reading index after concurrent saves: %v", err)
	}
	if len(entries) != n {
		t.Errorf("expected %d index entries, got %d", n, len(entries))
	}

	// All run files should exist
	matches, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	runFiles := 0
	for _, m := range matches {
		if filepath.Base(m) != "index.json" {
			runFiles++
		}
	}
	if runFiles != n {
		t.Errorf("expected %d run files, got %d", n, runFiles)
	}
}

// ---------------------------------------------------------------------------
// Graceful handling of corrupt/manually-edited JSON files
// ---------------------------------------------------------------------------

func TestCorruptIndexRebuild(t *testing.T) {
	dir := t.TempDir()
	backend := NewJSONBackend(dir)
	ctx := context.Background()

	// Save a valid run
	run := makeRun("https://example.com/valid", time.Now())
	backend.SaveRun(ctx, run)

	// Corrupt the index
	os.WriteFile(filepath.Join(dir, "index.json"), []byte("not valid json{{{"), 0644)

	// ListRuns should rebuild from files and still work
	results, err := backend.ListRuns(ctx, RunFilter{})
	if err != nil {
		t.Fatalf("ListRuns with corrupt index: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result after index rebuild, got %d", len(results))
	}
	if results[0].ID != run.ID {
		t.Errorf("wrong run after rebuild: got %s, want %s", results[0].ID, run.ID)
	}
}

func TestCorruptRunFileSkipped(t *testing.T) {
	dir := t.TempDir()
	backend := NewJSONBackendWithLogger(dir, nil) // suppress warnings in test output
	ctx := context.Background()

	// Save a valid run
	run := makeRun("https://example.com/good", time.Now())
	backend.SaveRun(ctx, run)

	// Write a corrupt run file
	os.WriteFile(filepath.Join(dir, "corrupt_20260415T120000Z_deadbeef.json"), []byte("{bad json"), 0644)

	// Corrupt the index to force a rebuild
	os.WriteFile(filepath.Join(dir, "index.json"), []byte("nope"), 0644)

	// ListRuns should skip the corrupt file and return the valid one
	results, err := backend.ListRuns(ctx, RunFilter{})
	if err != nil {
		t.Fatalf("ListRuns with corrupt run file: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result (corrupt file skipped), got %d", len(results))
	}
	if results[0].ID != run.ID {
		t.Errorf("wrong run: got %s, want %s", results[0].ID, run.ID)
	}
}

// ---------------------------------------------------------------------------
// Filename helpers
// ---------------------------------------------------------------------------

func TestSlugify(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://api.example.com/v1/posts", "api-example-com-v1-posts"},
		{"https://localhost:8080/health", "localhost-health"},
		{"https://example.com/", "example-com"},
		{"https://example.com", "example-com"},
		{"http://127.0.0.1:54978", "127-0-0-1"},
		{"http://127.0.0.1:54978/api/test", "127-0-0-1-api-test"},
	}
	for _, tt := range tests {
		got := slugify(tt.url)
		if got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestBuildFilename(t *testing.T) {
	run := BenchmarkRun{
		ID:        "abc12345-6789-0000-0000-000000000000",
		URL:       "https://api.example.com/posts",
		StartedAt: time.Date(2026, 4, 15, 10, 30, 12, 0, time.UTC),
	}
	got := buildFilename(run)
	want := "api-example-com-posts_20260415T103012Z_abc12345.json"
	if got != want {
		t.Errorf("buildFilename = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

func TestNewBackendFromConfig(t *testing.T) {
	cfg := &config.Config{
		Storage: config.StorageConfig{
			Mode: "local",
			Local: config.LocalStorageConfig{
				Driver: "json",
				JSON:   config.JSONDriverConfig{OutputDir: t.TempDir()},
			},
		},
	}

	backend, err := NewBackendFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewBackendFromConfig: %v", err)
	}
	if _, ok := backend.(*JSONBackend); !ok {
		t.Fatalf("expected *JSONBackend, got %T", backend)
	}
}

func TestNewBackendFromConfigUnimplemented(t *testing.T) {
	for _, driver := range []string{"postgres", "mysql"} {
		cfg := &config.Config{
			Storage: config.StorageConfig{
				Mode:  "local",
				Local: config.LocalStorageConfig{Driver: driver},
			},
		}
		_, err := NewBackendFromConfig(cfg)
		if err == nil {
			t.Errorf("expected error for unimplemented driver %s", driver)
		}
	}
}
