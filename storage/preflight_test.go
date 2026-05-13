package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mack-Overflow/api-bench/config"
)

func newPreflightCfg(baseURL, token string) *config.Config {
	return &config.Config{
		Cloud: config.CloudConfig{
			API_URL: baseURL,
			Token:   token,
		},
	}
}

func TestCloudPreflight_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/runs/preflight" || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer good" {
			t.Errorf("Authorization: got %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			WorkerSeconds int `json:"worker_seconds"`
		}
		_ = json.Unmarshal(body, &payload)
		if payload.WorkerSeconds != 40 {
			t.Errorf("worker_seconds: got %d want 40", payload.WorkerSeconds)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"allowed":true,"storage":{"allowed":true,"stored":3,"limit":50},"worker_seconds":{"allowed":true,"used":10,"limit":250,"remaining":240}}`))
	}))
	defer srv.Close()

	cfg := newPreflightCfg(srv.URL, "good")
	pf, err := CloudPreflight(context.Background(), cfg, PreflightOpts{WorkerSeconds: 40})
	if err != nil {
		t.Fatalf("CloudPreflight: %v", err)
	}
	if !pf.Allowed || !pf.Storage.Allowed || pf.Storage.Limit != 50 {
		t.Errorf("unexpected result: %+v", pf)
	}
	if pf.WorkerSeconds == nil || pf.WorkerSeconds.Remaining != 240 {
		t.Errorf("worker_seconds parse failed: %+v", pf.WorkerSeconds)
	}
}

func TestCloudPreflight_AuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"message":"Invalid API key"}`))
	}))
	defer srv.Close()

	cfg := newPreflightCfg(srv.URL, "bad")
	_, err := CloudPreflight(context.Background(), cfg, PreflightOpts{})
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
}

func TestCloudPreflight_StorageBlockedIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"allowed":true,"storage":{"allowed":false,"stored":50,"limit":50}}`))
	}))
	defer srv.Close()

	cfg := newPreflightCfg(srv.URL, "good")
	pf, err := CloudPreflight(context.Background(), cfg, PreflightOpts{})
	if err != nil {
		t.Fatalf("storage cap should not error, got %v", err)
	}
	if pf.Storage.Allowed {
		t.Errorf("expected storage.allowed=false, got true")
	}
}

func TestCloudPreflight_WorkerSecondsExceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"allowed":false,"error":"quota_exceeded","storage":{"allowed":true,"stored":1,"limit":50},"worker_seconds":{"allowed":false,"used":240,"limit":250,"remaining":10}}`))
	}))
	defer srv.Close()

	cfg := newPreflightCfg(srv.URL, "good")
	pf, err := CloudPreflight(context.Background(), cfg, PreflightOpts{WorkerSeconds: 100})
	if !errors.Is(err, ErrWorkerSecondsExceeded) {
		t.Fatalf("want ErrWorkerSecondsExceeded, got %v", err)
	}
	if pf.WorkerSeconds == nil || pf.WorkerSeconds.Remaining != 10 {
		t.Errorf("worker_seconds not parsed: %+v", pf.WorkerSeconds)
	}
}

func TestCloudPreflight_ForwardsAuthorizationHeader(t *testing.T) {
	got := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"allowed":true,"storage":{"allowed":true,"stored":0,"limit":50}}`))
	}))
	defer srv.Close()

	// No token in cfg — must come from the opts header.
	cfg := &config.Config{Cloud: config.CloudConfig{API_URL: srv.URL}}
	_, err := CloudPreflight(context.Background(), cfg, PreflightOpts{
		AuthorizationHeader: "Bearer forwarded-from-client",
	})
	if err != nil {
		t.Fatalf("CloudPreflight: %v", err)
	}
	if got != "Bearer forwarded-from-client" {
		t.Errorf("Authorization forwarded: got %q", got)
	}
}

func TestCloudPreflight_MissingAPIURL(t *testing.T) {
	cfg := &config.Config{Cloud: config.CloudConfig{}}
	_, err := CloudPreflight(context.Background(), cfg, PreflightOpts{})
	if err == nil || !strings.Contains(err.Error(), "api_url") {
		t.Fatalf("want api_url error, got %v", err)
	}
}
