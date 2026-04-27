package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeYAML(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "benchmarkr.yaml"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildRequestFromFlags(t *testing.T) {
	req, err := buildRequestFromFlags(buildOpts{
		URL:         "https://example.com/x",
		Method:      "post",
		Headers:     []string{"Authorization: Bearer abc"},
		Params:      []string{"limit=10"},
		Body:        `{"k":"v"}`,
		Concurrency: 3,
		Duration:    7,
		Name:        "",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if req.Method != "POST" {
		t.Errorf("method = %s", req.Method)
	}
	if req.Name != "https://example.com/x" {
		t.Errorf("default name = %s", req.Name)
	}
	if string(req.Body) != `{"k":"v"}` {
		t.Errorf("body = %s", req.Body)
	}
	var headers map[string]string
	json.Unmarshal(req.Headers, &headers)
	if headers["Authorization"] != "Bearer abc" {
		t.Errorf("headers = %+v", headers)
	}
}

func TestBuildRequestFromEndpoint_LocalConfig(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `version: 1
endpoints:
  - name: users
    method: GET
    url: https://api.example.com/users
    headers:
      X-Default: "1"
    defaults:
      concurrency: 7
      duration_seconds: 12
`)
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	req, err := buildRequestFromEndpoint(buildOpts{
		EndpointName: "users",
		Concurrency:  1,  // default value, not explicitly set
		Duration:     10, // default value, not explicitly set
		Method:       "GET",
		CacheMode:    "default",
		SetFlags:     map[string]bool{}, // nothing was explicitly set
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if req.URL != "https://api.example.com/users" {
		t.Errorf("URL = %s", req.URL)
	}
	if req.Concurrency != 7 {
		t.Errorf("concurrency = %d (defaults should apply)", req.Concurrency)
	}
	if req.DurationSec != 12 {
		t.Errorf("duration = %d (defaults should apply)", req.DurationSec)
	}
	if req.Name != "users" {
		t.Errorf("name = %q", req.Name)
	}
}

func TestBuildRequestFromEndpoint_CLIOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `version: 1
endpoints:
  - name: users
    method: GET
    url: https://api.example.com/users
    defaults:
      concurrency: 7
`)
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	req, err := buildRequestFromEndpoint(buildOpts{
		EndpointName: "users",
		Concurrency:  100,
		Duration:     5,
		Method:       "GET",
		CacheMode:    "default",
		SetFlags:     map[string]bool{"concurrency": true},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if req.Concurrency != 100 {
		t.Errorf("concurrency = %d (CLI flag should override default)", req.Concurrency)
	}
}

func TestBuildRequestFromEndpoint_HeaderMerge(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `version: 1
endpoints:
  - name: users
    method: GET
    url: https://api.example.com/users
    headers:
      X-Base: from-yaml
      X-Override: yaml
`)
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	req, err := buildRequestFromEndpoint(buildOpts{
		EndpointName: "users",
		Headers:      []string{"X-Override: cli", "X-CLI-Only: cli"},
		Concurrency:  1,
		Duration:     10,
		Method:       "GET",
		CacheMode:    "default",
		SetFlags:     map[string]bool{},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var headers map[string]string
	json.Unmarshal(req.Headers, &headers)
	if headers["X-Base"] != "from-yaml" {
		t.Errorf("X-Base lost: %+v", headers)
	}
	if headers["X-Override"] != "cli" {
		t.Errorf("X-Override should be CLI: %+v", headers)
	}
	if headers["X-CLI-Only"] != "cli" {
		t.Errorf("X-CLI-Only missing: %+v", headers)
	}
}

func TestBuildRequestFromEndpoint_MissingEndpoint(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `version: 1
endpoints:
  - {name: existing, method: GET, url: https://e.com}
`)
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	_, err := buildRequestFromEndpoint(buildOpts{
		EndpointName: "missing",
		Concurrency:  1,
		Duration:     10,
		Method:       "GET",
		CacheMode:    "default",
		SetFlags:     map[string]bool{},
	})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("expected missing-endpoint error, got %v", err)
	}
}

func TestBuildRequestFromEndpoint_VersionPinFallsBackWithoutCloud(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, `version: 1
endpoints:
  - name: users
    method: GET
    url: https://api.example.com/users
`)
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	t.Setenv("DB_URL", "")

	req, err := buildRequestFromEndpoint(buildOpts{
		EndpointName:    "users",
		EndpointVersion: 3,
		Concurrency:     1,
		Duration:        10,
		Method:          "GET",
		CacheMode:       "default",
		SetFlags:        map[string]bool{},
	})
	if err != nil {
		t.Fatalf("expected silent fallback, got error: %v", err)
	}
	if req.URL != "https://api.example.com/users" {
		t.Errorf("URL should fall back to local config, got %q", req.URL)
	}
	if req.EndpointVersionID != nil {
		t.Errorf("EndpointVersionID should be nil when cloud is unavailable, got %v", *req.EndpointVersionID)
	}
}
