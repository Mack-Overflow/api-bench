package storage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Mack-Overflow/api-bench/benchmark"
)

func sampleRun() BenchmarkRun {
	started := time.Date(2026, 5, 6, 14, 0, 0, 0, time.UTC)
	return BenchmarkRun{
		ID:             "00000000-0000-0000-0000-000000000001",
		Name:           "list-users",
		URL:            "https://api.example.com/v1/users",
		Method:         "GET",
		Headers:        json.RawMessage(`{"Accept":"application/json"}`),
		Params:         json.RawMessage(`{"page":"1"}`),
		Body:           json.RawMessage(`{}`),
		Concurrency:    4,
		DurationSec:    10,
		RateLimit:      100,
		ThrottleTimeMs: 0,
		CacheMode:      "default",
		StartedAt:      started,
		EndedAt:        started.Add(10 * time.Second),
		StopReason:     "completed",
		Result: benchmark.BenchmarkResult{
			Requests:         1000,
			Errors:           5,
			AvgMs:            14.2,
			P50Ms:            12,
			P95Ms:            32,
			P99Ms:            48,
			MinMs:            3,
			MaxMs:            90,
			AvgResponseBytes: 2048,
			MinResponseBytes: 1024,
			MaxResponseBytes: 4096,
			Status2xx:        995,
			Status4xx:        5,
		},
	}
}

func TestCloudBackend_SaveRun_Success(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotAuth   string
		gotCT     string
		gotBody   map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"run_id":1,"endpoint_id":1,"endpoint_version_id":1,"version":1}`))
	}))
	defer srv.Close()

	b, err := NewCloudBackend(srv.URL, "bmr_test_token")
	if err != nil {
		t.Fatalf("NewCloudBackend: %v", err)
	}

	if err := b.SaveRun(context.Background(), sampleRun()); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	if gotMethod != "POST" {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/runs" {
		t.Errorf("path = %q, want /api/runs", gotPath)
	}
	if gotAuth != "Bearer bmr_test_token" {
		t.Errorf("auth = %q, want Bearer bmr_test_token", gotAuth)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Errorf("content-type = %q, want application/json", gotCT)
	}

	checks := map[string]any{
		"name":             "list-users",
		"url":              "https://api.example.com/v1/users",
		"method":           "GET",
		"concurrency":      float64(4),
		"duration_seconds": float64(10),
		"rate_limit":       float64(100),
		"stop_reason":      "completed",
		"status":           "completed",
	}
	for k, want := range checks {
		if got := gotBody[k]; got != want {
			t.Errorf("payload[%q] = %v, want %v", k, got, want)
		}
	}

	result, ok := gotBody["result"].(map[string]any)
	if !ok {
		t.Fatalf("payload.result missing or not an object: %v", gotBody["result"])
	}
	if result["requests"] != float64(1000) {
		t.Errorf("result.requests = %v, want 1000", result["requests"])
	}
	if result["errors"] != float64(5) {
		t.Errorf("result.errors = %v, want 5", result["errors"])
	}
	if result["p95_ms"] != float64(32) {
		t.Errorf("result.p95_ms = %v, want 32", result["p95_ms"])
	}
	if result["avg_response_bytes"] != float64(2048) {
		t.Errorf("result.avg_response_bytes = %v, want 2048", result["avg_response_bytes"])
	}
}

func TestCloudBackend_SaveRun_NonSuccess_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"message":"validation failed","errors":{"name":["required"]}}`))
	}))
	defer srv.Close()

	b, _ := NewCloudBackend(srv.URL, "tok")
	err := b.SaveRun(context.Background(), sampleRun())
	if err == nil {
		t.Fatal("expected error on non-2xx response")
	}
	if !strings.Contains(err.Error(), "422") {
		t.Errorf("error %q should contain status 422", err.Error())
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("error %q should include response body", err.Error())
	}
}

func TestCloudBackend_SaveRun_AuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good_token" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"Invalid API key"}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	b, _ := NewCloudBackend(srv.URL, "bad_token")
	err := b.SaveRun(context.Background(), sampleRun())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 error, got %v", err)
	}
}

func TestCloudBackend_SaveRun_TrimsTrailingSlashOnBaseURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	b, err := NewCloudBackend(srv.URL+"/", "tok")
	if err != nil {
		t.Fatalf("NewCloudBackend: %v", err)
	}
	if err := b.SaveRun(context.Background(), sampleRun()); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	if gotPath != "/api/runs" {
		t.Errorf("path = %q, want /api/runs (no double slash)", gotPath)
	}
}

func TestNewCloudBackend_RequiresURLAndToken(t *testing.T) {
	if _, err := NewCloudBackend("", "tok"); err == nil {
		t.Error("expected error for empty URL")
	}
	if _, err := NewCloudBackend("https://x", ""); err == nil {
		t.Error("expected error for empty token")
	}
}

func TestCloudBackend_ListRuns_Success(t *testing.T) {
	run := sampleRun()
	summary := SummaryFromRun(run)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/runs" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer bmr_test_token" {
			t.Errorf("auth = %q, want Bearer bmr_test_token", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"data": []RunSummary{summary}})
	}))
	defer srv.Close()

	b, err := NewCloudBackend(srv.URL, "bmr_test_token")
	if err != nil {
		t.Fatalf("NewCloudBackend: %v", err)
	}

	results, err := b.ListRuns(context.Background(), RunFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != summary.Name {
		t.Errorf("name = %q, want %q", results[0].Name, summary.Name)
	}
	if results[0].AvgResponseBytes != summary.AvgResponseBytes {
		t.Errorf("avg_response_bytes = %d, want %d", results[0].AvgResponseBytes, summary.AvgResponseBytes)
	}
}

func TestCloudBackend_ListRuns_PassesFilters(t *testing.T) {
	var gotQuery url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"data": []RunSummary{}})
	}))
	defer srv.Close()

	b, _ := NewCloudBackend(srv.URL, "tok")
	since := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	filter := RunFilter{
		Endpoint: "api.example.com",
		Version:  3,
		Limit:    25,
		Since:    &since,
	}

	if _, err := b.ListRuns(context.Background(), filter); err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if gotQuery.Get("endpoint") != "api.example.com" {
		t.Errorf("endpoint param = %q, want api.example.com", gotQuery.Get("endpoint"))
	}
	if gotQuery.Get("version") != "3" {
		t.Errorf("version param = %q, want 3", gotQuery.Get("version"))
	}
	if gotQuery.Get("limit") != "25" {
		t.Errorf("limit param = %q, want 25", gotQuery.Get("limit"))
	}
	if gotQuery.Get("since") == "" {
		t.Error("expected since param to be set")
	}
}

func TestCloudBackend_ListRuns_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Invalid API key"}`))
	}))
	defer srv.Close()

	b, _ := NewCloudBackend(srv.URL, "bad_token")
	if _, err := b.ListRuns(context.Background(), RunFilter{}); err == nil {
		t.Fatal("expected error on 401 response")
	}
}

func TestCloudBackend_GetRun_Success(t *testing.T) {
	run := sampleRun()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/runs/"+run.ID {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer bmr_test_token" {
			t.Errorf("auth = %q, want Bearer bmr_test_token", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(run)
	}))
	defer srv.Close()

	b, err := NewCloudBackend(srv.URL, "bmr_test_token")
	if err != nil {
		t.Fatalf("NewCloudBackend: %v", err)
	}

	got, err := b.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.ID != run.ID {
		t.Errorf("id = %q, want %q", got.ID, run.ID)
	}
	if got.Name != run.Name {
		t.Errorf("name = %q, want %q", got.Name, run.Name)
	}
	if got.Result.Requests != run.Result.Requests {
		t.Errorf("requests = %d, want %d", got.Result.Requests, run.Result.Requests)
	}
}

func TestCloudBackend_GetRun_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Run not found"}`))
	}))
	defer srv.Close()

	b, _ := NewCloudBackend(srv.URL, "tok")
	_, err := b.GetRun(context.Background(), "999")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error %q should mention 'not found'", err.Error())
	}
}

func TestCloudBackend_DeleteRun_NotImplemented(t *testing.T) {
	b, _ := NewCloudBackend("https://x", "tok")
	if err := b.DeleteRun(context.Background(), "1"); err == nil {
		t.Error("expected DeleteRun to return not-implemented error")
	}
}
