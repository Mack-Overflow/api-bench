package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Mack-Overflow/api-bench/config"
)

var ErrAuth = errors.New("invalid or missing api key")

var ErrWorkerSecondsExceeded = errors.New("worker_seconds quota exceeded")

type PreflightResult struct {
	Allowed       bool          `json:"allowed"`
	Storage       StorageCheck  `json:"storage"`
	WorkerSeconds *WorkerCheck  `json:"worker_seconds,omitempty"`
}

type StorageCheck struct {
	Allowed bool `json:"allowed"`
	Stored  int  `json:"stored"`
	Limit   int  `json:"limit"`
}

type WorkerCheck struct {
	Allowed   bool `json:"allowed"`
	Used      int  `json:"used"`
	Limit     int  `json:"limit"`
	Remaining int  `json:"remaining"`
}

// PreflightOpts controls the preflight request body.
type PreflightOpts struct {
	WorkerSeconds       int
	AuthorizationHeader string
}


func CloudPreflight(ctx context.Context, cfg *config.Config, opts PreflightOpts) (PreflightResult, error) {
	if cfg == nil || strings.TrimSpace(cfg.Cloud.API_URL) == "" {
		return PreflightResult{}, fmt.Errorf("cloud api_url is empty")
	}

	auth := opts.AuthorizationHeader
	if auth == "" {
		token, err := ResolveCloudToken(cfg)
		if err != nil {
			return PreflightResult{}, err
		}
		auth = "Bearer " + token
	}

	body := struct {
		WorkerSeconds int `json:"worker_seconds,omitempty"`
	}{WorkerSeconds: opts.WorkerSeconds}

	buf, err := json.Marshal(body)
	if err != nil {
		return PreflightResult{}, fmt.Errorf("encode preflight: %w", err)
	}

	url := strings.TrimRight(cfg.Cloud.API_URL, "/") + "/api/runs/preflight"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return PreflightResult{}, fmt.Errorf("build preflight request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", auth)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return PreflightResult{}, fmt.Errorf("preflight request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))

	if resp.StatusCode == http.StatusUnauthorized {
		return PreflightResult{}, ErrAuth
	}

	var result PreflightResult
	if len(respBody) > 0 {
		_ = json.Unmarshal(respBody, &result)
	}

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		if result.WorkerSeconds != nil && !result.WorkerSeconds.Allowed {
			return result, ErrWorkerSecondsExceeded
		}
		return result, fmt.Errorf("preflight 429: %s", strings.TrimSpace(string(respBody)))
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return result, nil
	default:
		return result, fmt.Errorf("preflight returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
}
