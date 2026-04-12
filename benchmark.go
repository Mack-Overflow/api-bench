package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/Mack-Overflow/api-bench/benchmark"
	"github.com/Mack-Overflow/api-bench/db"
)

// serverRun wraps an ActiveRun with server-specific metadata.
type serverRun struct {
	*benchmark.ActiveRun
	dbID int64
}

var (
	runs   = make(map[string]*serverRun)
	runsMu sync.RWMutex
)

func startBenchmarkHandler(store *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req benchmark.StartBenchmarkRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := benchmark.ValidateRequest(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.EndpointID != nil && *req.EndpointID == 0 {
			req.EndpointID = nil
		}
		if req.EndpointVersionID != nil && *req.EndpointVersionID == 0 {
			req.EndpointVersionID = nil
		}

		runID := generateRunID()
		req.RunID = runID

		var endpointVersionID *int64
		var endpointID *int64

		benchmarkRunID, err := db.WithTx(store, func(tx *sql.Tx) (int64, error) {
			if req.EndpointID == nil {
				id, err := db.InsertEndpointTx(
					tx,
					req.Name,
					req.Method,
					req.URL,
					req.Headers,
					req.Params,
					req.Body,
					req.UserID,
				)
				if err != nil {
					return 0, err
				}
				endpointID = &id
			} else {
				endpointID = req.EndpointID
			}

			if req.ChangesMade || req.EndpointVersionID == nil {
				vid, err := db.InsertEndpointVersionTx(
					tx,
					*endpointID,
					1,
					req.Method,
					req.Headers,
					req.Params,
					req.Body,
					req.URL,
				)
				if err != nil {
					return 0, err
				}
				endpointVersionID = &vid
			} else {
				endpointVersionID = req.EndpointVersionID
			}

			return store.InsertBenchmarkRunTx(tx, db.BenchmarkRunInsert{
				EndpointVersionID: endpointVersionID,
				Concurrency:       req.Concurrency,
				RateLimit:         req.RateLimit,
				DurationSeconds:   req.DurationSec,
				ThrottleTimeMs:    req.ThrottleTimeMs,
				UserID:            req.UserID,
			})
		})
		if err != nil {
			log.Printf("insert benchmark run failed: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		activeRun := benchmark.Start(req)

		sr := &serverRun{
			ActiveRun: activeRun,
			dbID:      benchmarkRunID,
		}

		runsMu.Lock()
		runs[runID] = sr
		runsMu.Unlock()

		// Persist results to DB when benchmark completes
		go persistOnComplete(store, sr)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"run_id": runID,
		})
	}
}

func persistOnComplete(store *db.DB, sr *serverRun) {
	result, stopReason := sr.Wait()

	if err := store.MarkBenchmarkRunRunning(int(sr.dbID)); err != nil {
		log.Printf("failed to mark run running: %v", err)
	}

	_, err := db.WithTx(store, func(tx *sql.Tx) (int64, error) {
		if err := store.FinalizeBenchmarkRun(
			tx,
			int(sr.dbID),
			"completed",
			string(stopReason),
		); err != nil {
			return 0, err
		}

		return store.InsertBenchmarkMetrics(tx, db.BenchmarkMetricsInsert{
			BenchmarkRunID:   int(sr.dbID),
			Requests:         result.Requests,
			Errors:           result.Errors,
			AvgMs:            result.AvgMs,
			P50Ms:            result.P50Ms,
			P95Ms:            result.P95Ms,
			P99Ms:            result.P99Ms,
			MinMs:            result.MinMs,
			MaxMs:            result.MaxMs,
			AvgResponseBytes: result.AvgResponseBytes,
			MinResponseBytes: result.MinResponseBytes,
			MaxResponseBytes: result.MaxResponseBytes,
			Status2xx:        result.Status2xx,
			Status3xx:        result.Status3xx,
			Status4xx:        result.Status4xx,
			Status5xx:        result.Status5xx,
		})
	})

	if err != nil {
		log.Printf("failed to persist benchmark result: %v", err)
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
	})
}
