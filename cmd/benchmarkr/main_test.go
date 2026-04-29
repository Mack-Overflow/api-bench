package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// testBinary is the path to the compiled benchmarkr binary used by all tests.
var testBinary string

func TestMain(m *testing.M) {
	// Build the binary once for all tests.
	tmp, err := os.MkdirTemp("", "benchmarkr-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)

	bin := filepath.Join(tmp, "benchmarkr")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	cmd := exec.Command("go", "build", "-ldflags", `-X main.version=test-v1.2.3 -X main.commit=abc123`, "-o", bin, ".")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build binary: %v\n", err)
		os.Exit(1)
	}

	testBinary = bin
	os.Exit(m.Run())
}

// run executes the binary with args and returns stdout, stderr, and exit code.
func run(args ...string) (stdout, stderr string, exitCode int) {
	cmd := exec.Command(testBinary, args...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// runWithEnv executes the binary with extra environment variables set.
func runWithEnv(env map[string]string, args ...string) (stdout, stderr string, exitCode int) {
	cmd := exec.Command(testBinary, args...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	// Copy current env, then override specified keys
	envList := os.Environ()
	for k, v := range env {
		prefix := k + "="
		found := false
		for i, e := range envList {
			if strings.HasPrefix(e, prefix) {
				envList[i] = prefix + v
				found = true
				break
			}
		}
		if !found {
			envList = append(envList, prefix+v)
		}
	}
	cmd.Env = envList

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// writeTestConfig writes a minimal config TOML to a temp file and returns its path.
func writeTestConfig(t *testing.T, mode, driver string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := fmt.Sprintf("[storage]\nmode = %q\n\n[storage.local]\ndriver = %q\n\n[cloud]\napi_url = \"https://test.example.com\"\ntoken_env = \"BENCH_CLOUD_TOKEN\"\n", mode, driver)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// newTestServer returns a simple HTTP server that records request details.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := map[string]any{
			"method":  r.Method,
			"path":    r.URL.Path,
			"query":   r.URL.RawQuery,
			"headers": map[string]string{},
		}
		for k := range r.Header {
			resp["headers"].(map[string]string)[k] = r.Header.Get(k)
		}
		json.NewEncoder(w).Encode(resp)
	}))
}

// ---------------------------------------------------------------------------
// Build
// ---------------------------------------------------------------------------

func TestBuild(t *testing.T) {
	if _, err := os.Stat(testBinary); err != nil {
		t.Fatalf("binary does not exist at %s: %v", testBinary, err)
	}
}

// ---------------------------------------------------------------------------
// Top-level commands
// ---------------------------------------------------------------------------

func TestNoArgs(t *testing.T) {
	stdout, _, code := run()
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stdout, "benchmarkr") || !strings.Contains(stdout, "Usage:") {
		t.Fatalf("expected usage output, got:\n%s", stdout)
	}
}

func TestHelp(t *testing.T) {
	for _, arg := range []string{"help", "--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			stdout, _, code := run(arg)
			if code != 0 {
				t.Fatalf("expected exit 0 for %q, got %d", arg, code)
			}
			if !strings.Contains(stdout, "Usage:") {
				t.Fatalf("expected usage text for %q, got:\n%s", arg, stdout)
			}
			if !strings.Contains(stdout, "run") {
				t.Fatalf("expected 'run' command listed for %q", arg)
			}
		})
	}
}

func TestVersion(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		t.Run(arg, func(t *testing.T) {
			stdout, _, code := run(arg)
			if code != 0 {
				t.Fatalf("expected exit 0 for %q, got %d", arg, code)
			}
			if !strings.Contains(stdout, "test-v1.2.3") {
				t.Fatalf("expected version string for %q, got:\n%s", arg, stdout)
			}
		})
	}
}

func TestUnknownCommand(t *testing.T) {
	_, stderr, code := run("foobar")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Fatalf("expected 'unknown command' in stderr, got:\n%s", stderr)
	}
}

// ---------------------------------------------------------------------------
// run: missing required flags
// ---------------------------------------------------------------------------

func TestRunMissingURL(t *testing.T) {
	_, stderr, code := run("run")
	if code == 0 {
		t.Fatal("expected non-zero exit code when --url is missing")
	}
	if !strings.Contains(stderr, "provide --url, --endpoint, or --all") {
		t.Fatalf("expected 'provide --url, --endpoint, or --all' in stderr, got:\n%s", stderr)
	}
}

// ---------------------------------------------------------------------------
// run: basic execution with defaults
// ---------------------------------------------------------------------------

func TestRunBasicDefaults(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	stdout, stderr, code := run("run", "--url", ts.URL, "--duration", "1")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	// Human-readable output should contain the results banner
	if !strings.Contains(stdout, "Results") {
		t.Fatalf("expected 'Results' in output, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Total Requests:") {
		t.Fatalf("expected 'Total Requests:' in output, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Latency:") {
		t.Fatalf("expected 'Latency:' in output, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Stop Reason:") {
		t.Fatalf("expected 'Stop Reason:' in output, got:\n%s", stdout)
	}
}

// ---------------------------------------------------------------------------
// run --json
// ---------------------------------------------------------------------------

func TestRunJSON(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	stdout, _, code := run("run", "--url", ts.URL, "--duration", "1", "--json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}

	var result struct {
		StopReason string `json:"stop_reason"`
		Duration   string `json:"duration"`
		Stored     bool   `json:"stored"`
		Result     struct {
			Requests  int     `json:"requests"`
			Errors    int     `json:"errors_total"`
			AvgMs     float64 `json:"avg_ms"`
			P50Ms     int64   `json:"p50_ms"`
			P95Ms     int64   `json:"p95_ms"`
			P99Ms     int64   `json:"p99_ms"`
			Status2xx int     `json:"status_2xx"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("failed to parse JSON output: %v\nraw:\n%s", err, stdout)
	}
	if result.StopReason != "completed" {
		t.Fatalf("expected stop_reason=completed, got %q", result.StopReason)
	}
	if result.Result.Requests == 0 {
		t.Fatal("expected at least 1 request")
	}
	if result.Result.Status2xx == 0 {
		t.Fatal("expected at least 1 2xx response")
	}
	if result.Stored {
		t.Fatal("expected stored=false when --store not set")
	}
}

// ---------------------------------------------------------------------------
// run --method
// ---------------------------------------------------------------------------

func TestRunMethod(t *testing.T) {
	var lastMethod atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastMethod.Store(r.Method)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "{}")
	}))
	defer ts.Close()

	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		t.Run(method, func(t *testing.T) {
			_, _, code := run("run", "--url", ts.URL, "--method", method, "--duration", "1", "--json")
			if code != 0 {
				t.Fatalf("exit %d for method %s", code, method)
			}
			got := lastMethod.Load().(string)
			if got != method {
				t.Fatalf("expected method %s, server saw %s", method, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// run --concurrency
// ---------------------------------------------------------------------------

func TestRunConcurrency(t *testing.T) {
	var maxConcurrent atomic.Int32
	var current atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := current.Add(1)
		for {
			old := maxConcurrent.Load()
			if c <= old || maxConcurrent.CompareAndSwap(old, c) {
				break
			}
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "{}")
		current.Add(-1)
	}))
	defer ts.Close()

	stdout, _, code := run("run", "--url", ts.URL, "--concurrency", "4", "--duration", "2", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}

	var result struct {
		Result struct {
			Requests int `json:"requests"`
		} `json:"result"`
	}
	json.Unmarshal([]byte(stdout), &result)

	if result.Result.Requests == 0 {
		t.Fatal("expected requests > 0 with concurrency=4")
	}
}

// ---------------------------------------------------------------------------
// run --duration
// ---------------------------------------------------------------------------

func TestRunDuration(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	stdout, _, code := run("run", "--url", ts.URL, "--duration", "2", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}

	var result struct {
		Duration string `json:"duration"`
	}
	json.Unmarshal([]byte(stdout), &result)

	// Duration should contain "2s" or "2." (e.g. "2.001s")
	if !strings.HasPrefix(result.Duration, "2") && !strings.HasPrefix(result.Duration, "1.9") {
		t.Fatalf("expected ~2s duration, got %q", result.Duration)
	}
}

// ---------------------------------------------------------------------------
// run --rate-limit
// ---------------------------------------------------------------------------

func TestRunRateLimit(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	stdout, _, code := run("run", "--url", ts.URL, "--rate-limit", "5", "--duration", "2", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}

	var result struct {
		Result struct {
			Requests int `json:"requests"`
		} `json:"result"`
	}
	json.Unmarshal([]byte(stdout), &result)

	// With rate-limit=5 and duration=2s, expect roughly 10 requests (5/s * 2s).
	// Allow a generous range to avoid flaky tests.
	if result.Result.Requests > 15 {
		t.Fatalf("rate limit 5 req/s over 2s should yield ~10 requests, got %d", result.Result.Requests)
	}
}

// ---------------------------------------------------------------------------
// run --throttle
// ---------------------------------------------------------------------------

func TestRunThrottle(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	stdout, _, code := run("run", "--url", ts.URL, "--throttle", "200", "--duration", "1", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}

	var result struct {
		Result struct {
			Requests int `json:"requests"`
		} `json:"result"`
	}
	json.Unmarshal([]byte(stdout), &result)

	// With 200ms throttle over 1s, expect at most ~5 requests per worker.
	if result.Result.Requests > 10 {
		t.Fatalf("throttle 200ms should limit requests, got %d", result.Result.Requests)
	}
}

// ---------------------------------------------------------------------------
// run --cache-mode
// ---------------------------------------------------------------------------

func TestRunCacheMode(t *testing.T) {
	var sawCacheControl atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawCacheControl.Store(r.Header.Get("Cache-Control"))
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "{}")
	}))
	defer ts.Close()

	t.Run("bypass", func(t *testing.T) {
		_, _, code := run("run", "--url", ts.URL, "--cache-mode", "bypass", "--duration", "1", "--json")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		cc := sawCacheControl.Load().(string)
		if !strings.Contains(cc, "no-cache") {
			t.Fatalf("expected Cache-Control=no-cache for bypass mode, got %q", cc)
		}
	})

	t.Run("default", func(t *testing.T) {
		sawCacheControl.Store("")
		_, _, code := run("run", "--url", ts.URL, "--cache-mode", "default", "--duration", "1", "--json")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		cc := sawCacheControl.Load().(string)
		if strings.Contains(cc, "no-cache") {
			t.Fatalf("expected no Cache-Control=no-cache for default mode, got %q", cc)
		}
	})
}

// ---------------------------------------------------------------------------
// run --name
// ---------------------------------------------------------------------------

func TestRunName(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// --name should appear in the banner output (non-JSON mode)
	stdout, _, code := run("run", "--url", ts.URL, "--name", "my-custom-benchmark", "--duration", "1")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	// The banner prints the target URL line — with --name the URL is still printed.
	// Just verify it ran successfully. The name is used internally for storage.
	if !strings.Contains(stdout, "Results") {
		t.Fatalf("expected results output, got:\n%s", stdout)
	}
}

// ---------------------------------------------------------------------------
// run --header
// ---------------------------------------------------------------------------

func TestRunHeader(t *testing.T) {
	var sawHeaders atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHeaders.Store(map[string]string{
			"X-Custom-One": r.Header.Get("X-Custom-One"),
			"X-Custom-Two": r.Header.Get("X-Custom-Two"),
		})
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "{}")
	}))
	defer ts.Close()

	_, _, code := run("run", "--url", ts.URL,
		"--header", "X-Custom-One: value-one",
		"--header", "X-Custom-Two: value-two",
		"--duration", "1", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}

	hdrs := sawHeaders.Load().(map[string]string)
	if hdrs["X-Custom-One"] != "value-one" {
		t.Fatalf("expected X-Custom-One=value-one, got %q", hdrs["X-Custom-One"])
	}
	if hdrs["X-Custom-Two"] != "value-two" {
		t.Fatalf("expected X-Custom-Two=value-two, got %q", hdrs["X-Custom-Two"])
	}
}

// ---------------------------------------------------------------------------
// run --param
// ---------------------------------------------------------------------------

func TestRunParam(t *testing.T) {
	var sawPage, sawLimit atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPage.Store(r.URL.Query().Get("page"))
		sawLimit.Store(r.URL.Query().Get("limit"))
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "{}")
	}))
	defer ts.Close()

	_, _, code := run("run", "--url", ts.URL,
		"--param", "page=1",
		"--param", "limit=50",
		"--duration", "1", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}

	page, _ := sawPage.Load().(string)
	limit, _ := sawLimit.Load().(string)
	if page != "1" {
		t.Fatalf("expected page=1, got %q", page)
	}
	if limit != "50" {
		t.Fatalf("expected limit=50, got %q", limit)
	}
}

// ---------------------------------------------------------------------------
// run --body
// ---------------------------------------------------------------------------

func TestRunBody(t *testing.T) {
	var sawBody atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		sawBody.Store(body)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "{}")
	}))
	defer ts.Close()

	_, _, code := run("run", "--url", ts.URL,
		"--method", "POST",
		"--body", `{"name":"test","count":42}`,
		"--duration", "1", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}

	body, ok := sawBody.Load().(map[string]any)
	if !ok || body == nil {
		t.Fatal("no body captured")
	}
	if body["name"] != "test" {
		t.Fatalf("expected name=test, got %v", body["name"])
	}
	if body["count"] != float64(42) {
		t.Fatalf("expected count=42, got %v", body["count"])
	}
}

// ---------------------------------------------------------------------------
// run --store without DB_URL
// ---------------------------------------------------------------------------

func TestRunStoreWithoutConfig(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// Point BENCH_CONFIG to a nonexistent file so no config is found
	env := map[string]string{"BENCH_CONFIG": filepath.Join(t.TempDir(), "nope.toml")}
	_, stderr, code := runWithEnv(env, "run", "--url", ts.URL, "--duration", "1", "--store")
	if code == 0 {
		t.Fatal("expected non-zero exit when --store used without config")
	}
	if !strings.Contains(stderr, "No storage configured") {
		t.Fatalf("expected 'No storage configured' error, got:\n%s", stderr)
	}
}

func TestRunStoreShortFlagWithoutConfig(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// -s is shorthand for --store, should produce same config error
	env := map[string]string{"BENCH_CONFIG": filepath.Join(t.TempDir(), "nope.toml")}
	_, stderr, code := runWithEnv(env, "run", "--url", ts.URL, "--duration", "1", "-s")
	if code == 0 {
		t.Fatal("expected non-zero exit when -s used without config")
	}
	if !strings.Contains(stderr, "No storage configured") {
		t.Fatalf("expected 'No storage configured' error, got:\n%s", stderr)
	}
}

func TestRunStoreWithCloudConfigNoDBURL(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	cfgPath := writeTestConfig(t, "cloud", "")
	env := map[string]string{"BENCH_CONFIG": cfgPath, "DB_URL": ""}

	// Benchmark should run, but persistence fails with DB_URL warning
	stdout, stderr, code := runWithEnv(env, "run", "--url", ts.URL, "--duration", "1", "--store")
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "DB_URL") {
		t.Fatalf("expected warning about DB_URL, got:\nstderr: %s", stderr)
	}
	// Results should still be printed
	if !strings.Contains(stdout, "Results") {
		t.Fatalf("expected results output even when persistence fails, got:\n%s", stdout)
	}
}

func TestRunStoreWithLocalJSONConfig(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	outputDir := t.TempDir()
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	content := fmt.Sprintf("[storage]\nmode = \"local\"\n\n[storage.local]\ndriver = \"json\"\n\n[storage.local.json]\noutput_dir = %q\n", outputDir)
	os.WriteFile(cfgPath, []byte(content), 0644)
	env := map[string]string{"BENCH_CONFIG": cfgPath}

	stdout, stderr, code := runWithEnv(env, "run", "--url", ts.URL, "--duration", "1", "--store")
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if strings.Contains(stderr, "warning") {
		t.Fatalf("unexpected warning in stderr: %s", stderr)
	}
	if !strings.Contains(stdout, "Results stored") {
		t.Fatalf("expected 'Results stored' message, got:\n%s", stdout)
	}

	// Verify a run file was written
	matches, _ := filepath.Glob(filepath.Join(outputDir, "*.json"))
	runFiles := 0
	for _, m := range matches {
		if filepath.Base(m) != "index.json" {
			runFiles++
		}
	}
	if runFiles != 1 {
		t.Fatalf("expected 1 run file in %s, got %d", outputDir, runFiles)
	}
}

// ---------------------------------------------------------------------------
// run --all
// ---------------------------------------------------------------------------

func TestRunAll(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "benchmarkr.yaml")
	yaml := fmt.Sprintf(`version: 1
endpoints:
  - name: alpha
    method: GET
    url: %s/alpha
  - name: beta
    method: GET
    url: %s/beta
`, ts.URL, ts.URL)
	if err := os.WriteFile(cfgPath, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := run("run", "--all", "--file", cfgPath, "--duration", "1", "--json")
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	var results []struct {
		Name       string `json:"name"`
		StopReason string `json:"stop_reason"`
		Result     struct {
			Requests int `json:"requests"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(stdout), &results); err != nil {
		t.Fatalf("output is not a JSON array: %v\nraw:\n%s", err, stdout)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(results), results)
	}
	if results[0].Name != "alpha" || results[1].Name != "beta" {
		t.Fatalf("expected runs in declaration order [alpha, beta], got [%s, %s]", results[0].Name, results[1].Name)
	}
	for _, r := range results {
		if r.StopReason != "completed" {
			t.Errorf("run %q stop_reason = %q, want completed", r.Name, r.StopReason)
		}
		if r.Result.Requests == 0 {
			t.Errorf("run %q had 0 requests", r.Name)
		}
	}
}

func TestRunAllConflicts(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"with --url", []string{"run", "--all", "--url", ts.URL, "--duration", "1"}, "--all and --url"},
		{"with --endpoint", []string{"run", "--all", "-e", "x", "--duration", "1"}, "--all and --endpoint"},
		{"with --version", []string{"run", "--all", "-v", "2", "--duration", "1"}, "--version is not supported with --all"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := run(tc.args...)
			if code == 0 {
				t.Fatalf("expected non-zero exit, stderr: %s", stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("expected %q in stderr, got: %s", tc.want, stderr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// run --json suppresses banner
// ---------------------------------------------------------------------------

func TestRunJSONSuppressesBanner(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	stdout, _, code := run("run", "--url", ts.URL, "--duration", "1", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	// JSON mode should NOT contain the human-readable banner
	if strings.Contains(stdout, "Benchmarkr - API Performance Testing") {
		t.Fatal("--json should suppress the human-readable banner")
	}
	// But it should be valid JSON
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Error cases
// ---------------------------------------------------------------------------

func TestRunInvalidBodyJSON(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	_, stderr, code := run("run", "--url", ts.URL, "--body", "not-json", "--duration", "1")
	if code == 0 {
		t.Fatal("expected non-zero exit for invalid body JSON")
	}
	if !strings.Contains(stderr, "must be valid JSON") {
		t.Fatalf("expected 'must be valid JSON' error, got:\n%s", stderr)
	}
}

func TestRunInvalidHeaderFormat(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	_, stderr, code := run("run", "--url", ts.URL, "--header", "no-colon-here", "--duration", "1")
	if code == 0 {
		t.Fatal("expected non-zero exit for invalid header format")
	}
	if !strings.Contains(stderr, "invalid header format") {
		t.Fatalf("expected 'invalid header format' error, got:\n%s", stderr)
	}
}

func TestRunInvalidParamFormat(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	_, stderr, code := run("run", "--url", ts.URL, "--param", "no-equals", "--duration", "1")
	if code == 0 {
		t.Fatal("expected non-zero exit for invalid param format")
	}
	if !strings.Contains(stderr, "invalid param format") {
		t.Fatalf("expected 'invalid param format' error, got:\n%s", stderr)
	}
}

// ---------------------------------------------------------------------------
// run: banner content verification
// ---------------------------------------------------------------------------

func TestRunBannerContent(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	stdout, _, code := run("run", "--url", ts.URL,
		"--concurrency", "3",
		"--duration", "1",
		"--rate-limit", "10",
		"--throttle", "50",
		"--cache-mode", "bypass",
	)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}

	checks := []string{
		"Benchmarkr - API Performance Testing",
		"Workers:     3",
		"Duration:    1s",
		"Rate Limit:  10 req/s",
		"Throttle:    50ms",
		"Cache Mode:  bypass",
	}
	for _, want := range checks {
		if !strings.Contains(stdout, want) {
			t.Errorf("banner missing %q\nfull output:\n%s", want, stdout)
		}
	}
}

// ---------------------------------------------------------------------------
// run: status code tracking in JSON output
// ---------------------------------------------------------------------------

func TestRunStatusCodeTracking(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"ok":true}`)
	}))
	defer ts.Close()

	stdout, _, code := run("run", "--url", ts.URL, "--duration", "1", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}

	var result struct {
		Result struct {
			Status2xx int `json:"status_2xx"`
			Status3xx int `json:"status_3xx"`
			Status4xx int `json:"status_4xx"`
			Status5xx int `json:"status_5xx"`
		} `json:"result"`
	}
	json.Unmarshal([]byte(stdout), &result)

	if result.Result.Status2xx == 0 {
		t.Fatal("expected status_2xx > 0")
	}
	if result.Result.Status3xx != 0 || result.Result.Status4xx != 0 || result.Result.Status5xx != 0 {
		t.Fatalf("expected only 2xx responses, got 3xx=%d 4xx=%d 5xx=%d",
			result.Result.Status3xx, result.Result.Status4xx, result.Result.Status5xx)
	}
}

// ---------------------------------------------------------------------------
// run: response size tracking
// ---------------------------------------------------------------------------

func TestRunResponseSizeTracking(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Write a known-size response
		fmt.Fprintln(w, `{"data":"hello world"}`)
	}))
	defer ts.Close()

	stdout, _, code := run("run", "--url", ts.URL, "--duration", "1", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}

	var result struct {
		Result struct {
			AvgResponseBytes int64 `json:"avg_response_bytes"`
			MinResponseBytes int64 `json:"min_response_bytes"`
			MaxResponseBytes int64 `json:"max_response_bytes"`
		} `json:"result"`
	}
	json.Unmarshal([]byte(stdout), &result)

	if result.Result.AvgResponseBytes == 0 {
		t.Fatal("expected avg_response_bytes > 0")
	}
	if result.Result.MinResponseBytes == 0 {
		t.Fatal("expected min_response_bytes > 0")
	}
	if result.Result.MaxResponseBytes == 0 {
		t.Fatal("expected max_response_bytes > 0")
	}
}

// ---------------------------------------------------------------------------
// run: latency metrics are populated
// ---------------------------------------------------------------------------

func TestRunLatencyMetrics(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	stdout, _, code := run("run", "--url", ts.URL, "--duration", "1", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}

	var result struct {
		Result struct {
			AvgMs float64 `json:"avg_ms"`
			P50Ms int64   `json:"p50_ms"`
			P95Ms int64   `json:"p95_ms"`
			P99Ms int64   `json:"p99_ms"`
			MinMs float64 `json:"min_ms"`
			MaxMs float64 `json:"max_ms"`
		} `json:"result"`
	}
	json.Unmarshal([]byte(stdout), &result)

	if result.Result.MaxMs < result.Result.MinMs {
		t.Fatalf("max_ms (%f) < min_ms (%f)", result.Result.MaxMs, result.Result.MinMs)
	}
}

// ---------------------------------------------------------------------------
// config subcommand
// ---------------------------------------------------------------------------

func TestConfigNoArgs(t *testing.T) {
	stdout, _, code := run("config")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(stdout, "benchmarkr config") {
		t.Fatalf("expected config usage text, got:\n%s", stdout)
	}
}

func TestConfigHelp(t *testing.T) {
	stdout, _, code := run("config", "help")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(stdout, "init") || !strings.Contains(stdout, "show") {
		t.Fatalf("expected config help listing subcommands, got:\n%s", stdout)
	}
}

func TestConfigSetAndGet(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	env := map[string]string{"BENCH_CONFIG": cfgPath}

	// set creates a new config from defaults when none exists
	stdout, _, code := runWithEnv(env, "config", "set", "storage.mode", "cloud")
	if code != 0 {
		t.Fatalf("config set exit %d\nstdout: %s", code, stdout)
	}
	if !strings.Contains(stdout, "storage.mode = cloud") {
		t.Fatalf("expected confirmation, got:\n%s", stdout)
	}

	// get reads back the value
	stdout, _, code = runWithEnv(env, "config", "get", "storage.mode")
	if code != 0 {
		t.Fatalf("config get exit %d", code)
	}
	if strings.TrimSpace(stdout) != "cloud" {
		t.Fatalf("expected 'cloud', got %q", strings.TrimSpace(stdout))
	}
}

func TestConfigSetValidation(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	env := map[string]string{"BENCH_CONFIG": cfgPath}

	_, stderr, code := runWithEnv(env, "config", "set", "storage.mode", "invalid")
	if code == 0 {
		t.Fatal("expected non-zero exit for invalid storage.mode value")
	}
	if !strings.Contains(stderr, "must be") {
		t.Fatalf("expected validation error, got:\n%s", stderr)
	}
}

func TestConfigShow(t *testing.T) {
	cfgPath := writeTestConfig(t, "local", "json")
	env := map[string]string{"BENCH_CONFIG": cfgPath}

	stdout, _, code := runWithEnv(env, "config", "show")
	if code != 0 {
		t.Fatalf("config show exit %d", code)
	}
	if !strings.Contains(stdout, "Resolved from:") {
		t.Fatalf("expected source annotation, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, `mode = "local"`) {
		t.Fatalf("expected storage.mode in output, got:\n%s", stdout)
	}
}

func TestConfigGetWithoutConfig(t *testing.T) {
	env := map[string]string{"BENCH_CONFIG": filepath.Join(t.TempDir(), "nope.toml")}

	_, stderr, code := runWithEnv(env, "config", "get", "storage.mode")
	if code == 0 {
		t.Fatal("expected non-zero exit when config doesn't exist")
	}
	if !strings.Contains(stderr, "BENCH_CONFIG") {
		t.Fatalf("expected error about missing config, got:\n%s", stderr)
	}
}

func TestConfigTestJSON(t *testing.T) {
	dir := t.TempDir()
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")

	content := fmt.Sprintf("[storage]\nmode = \"local\"\n\n[storage.local]\ndriver = \"json\"\n\n[storage.local.json]\noutput_dir = %q\n", dir)
	os.WriteFile(cfgPath, []byte(content), 0644)
	env := map[string]string{"BENCH_CONFIG": cfgPath}

	stdout, _, code := runWithEnv(env, "config", "test")
	if code != 0 {
		t.Fatalf("config test exit %d", code)
	}
	if !strings.Contains(stdout, "JSON storage OK") {
		t.Fatalf("expected 'JSON storage OK', got:\n%s", stdout)
	}
}

func TestConfigUnknownSubcommand(t *testing.T) {
	_, stderr, code := run("config", "foobar")
	if code == 0 {
		t.Fatal("expected non-zero exit for unknown config subcommand")
	}
	if !strings.Contains(stderr, "unknown config command") {
		t.Fatalf("expected 'unknown config command' error, got:\n%s", stderr)
	}
}

func TestHelpListsConfigCommand(t *testing.T) {
	stdout, _, code := run("help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout, "config") {
		t.Fatalf("expected 'config' in help output, got:\n%s", stdout)
	}
}
