package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/Mack-Overflow/api-bench/benchmark"
	"github.com/Mack-Overflow/api-bench/config"
	"github.com/Mack-Overflow/api-bench/storage"
)

// serverRun wraps an ActiveRun with server-specific metadata. The full
// lifecycle lives in memory; persistence happens once at completion through
// the configured storage.StorageBackend. skipPersist is set when Laravel's
// preflight reported the user has hit their stored-runs cap — the benchmark
// still runs, but the result is not saved.
type serverRun struct {
	*benchmark.ActiveRun
	req         benchmark.StartBenchmarkRequest
	userID      int64
	runID       string
	skipPersist bool
}

var (
	runs           = make(map[string]*serverRun)
	activeUserRuns = make(map[int64]string) // userID → runID (one active run per user)
	runsMu         sync.RWMutex
)

const maxRequestBodyBytes int64 = 1 << 20 // 1 MB — benchmark configs are small

func startBenchmarkHandler(backend storage.StorageBackend, preflightCfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		userID, ok := r.Context().Value(userIDKey).(int64)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		runsMu.RLock()
		if existingRunID, active := activeUserRuns[userID]; active {
			if sr, exists := runs[existingRunID]; exists && sr.GetStatus() == benchmark.StatusRunning {
				runsMu.RUnlock()
				http.Error(w, "a benchmark is already running — stop it before starting another", http.StatusConflict)
				return
			}
		}
		runsMu.RUnlock()

		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

		var req benchmark.StartBenchmarkRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		if err := validateBenchmarkURL(req.URL); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		benchmark.ApplyLimits(&req, benchmark.DefaultLimits)

		if err := benchmark.ValidateRequest(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		skipPersist, ok := runPreflight(w, r, preflightCfg, req)
		if !ok {
			return
		}

		req.UserID = &userID
		runID := generateRunID()
		req.RunID = runID

		activeRun := benchmark.Start(req)

		sr := &serverRun{
			ActiveRun:   activeRun,
			req:         req,
			userID:      userID,
			runID:       runID,
			skipPersist: skipPersist,
		}

		runsMu.Lock()
		runs[runID] = sr
		activeUserRuns[userID] = runID
		runsMu.Unlock()

		go persistOnComplete(backend, sr)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"run_id": runID,
		})
	}
}

// runPreflight calls Laravel's POST /api/runs/preflight. Returns
// (skipPersist, ok). When ok=false the response has already been written
// (4xx/5xx for the client). When preflightCfg is nil (LARAVEL_INTERNAL_URL
// unset), the preflight is skipped entirely.
func runPreflight(w http.ResponseWriter, r *http.Request, preflightCfg *config.Config, req benchmark.StartBenchmarkRequest) (bool, bool) {
	if preflightCfg == nil {
		return false, true
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	pf, err := storage.CloudPreflight(ctx, preflightCfg, storage.PreflightOpts{
		WorkerSeconds:       req.Concurrency * req.DurationSec,
		AuthorizationHeader: r.Header.Get("Authorization"),
	})
	switch {
	case errors.Is(err, storage.ErrAuth):
		http.Error(w, "invalid api key", http.StatusUnauthorized)
		return false, false
	case errors.Is(err, storage.ErrWorkerSecondsExceeded):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":          "quota_exceeded",
			"worker_seconds": pf.WorkerSeconds,
		})
		return false, false
	case err != nil:
		log.Printf("preflight failed: %v", err)
		http.Error(w, "preflight failed", http.StatusBadGateway)
		return false, false
	}
	return !pf.Storage.Allowed, true
}

// persistOnComplete waits for the benchmark to finish, releases the
// per-user active slot, and writes the run to the configured storage
// backend. Persistence is a single SaveRun call — the backend chooses
// how to record it (SQL upsert, JSON file, or HTTP POST to Laravel).
// When skipPersist is true (storage cap reached), the save is suppressed.
func persistOnComplete(backend storage.StorageBackend, sr *serverRun) {
	result, stopReason := sr.Wait()

	runsMu.Lock()
	if activeUserRuns[sr.userID] == sr.runID {
		delete(activeUserRuns, sr.userID)
	}
	runsMu.Unlock()

	if sr.skipPersist {
		return
	}

	run := storage.BenchmarkRun{
		ID:             sr.runID,
		UserID:         &sr.userID,
		Name:           sr.req.Name,
		URL:            sr.req.URL,
		Method:         sr.req.Method,
		Headers:        sr.req.Headers,
		Params:         sr.req.Params,
		Body:           sr.req.Body,
		Concurrency:    sr.req.Concurrency,
		DurationSec:    sr.req.DurationSec,
		RateLimit:      sr.req.RateLimit,
		ThrottleTimeMs: sr.req.ThrottleTimeMs,
		CacheMode:      string(sr.req.CacheMode),
		StartedAt:      sr.StartedAt,
		EndedAt:        time.Now(),
		StopReason:     string(stopReason),
		Result:         *result,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := backend.SaveRun(ctx, run); err != nil {
		log.Printf("failed to persist benchmark run %s: %v", sr.runID, err)
	}
}

func stopBenchmarkHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	runsMu.RLock()
	sr, ok := runs[id]
	runsMu.RUnlock()

	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	sr.Cancel()

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "stopping",
	})
}

func getBenchmarkStatusHandler(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	runsMu.RLock()
	sr, ok := runs[id]
	runsMu.RUnlock()

	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"Status":      sr.GetStatus(),
		"stop_reason": sr.GetStopReason(),
		"Result":      sr.GetResult(),
		"StartedAt":   sr.StartedAt,
		"EndedAt":     sr.GetEndedAt(),
		"persisted":   !sr.skipPersist,
	})
}
