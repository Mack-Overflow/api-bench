package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Mack-Overflow/api-bench/benchmark"
)

func benchmarkStreamHandler(w http.ResponseWriter, r *http.Request) {
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

	// SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	ctx := r.Context()
	logCursor := 0

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			var snap benchmark.MetricsSnapshot
			snap, logCursor = sr.SnapshotLogs(logCursor)

			payload, _ := json.Marshal(snap)

			fmt.Fprintf(w, "event: metrics\n")
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()

			if sr.GetStatus() == benchmark.StatusCompleted {
				payload, _ := json.Marshal(map[string]any{
					"reason": sr.GetStopReason(),
				})
				fmt.Fprintf(w, "event: done\n")
				fmt.Fprintf(w, "data: %s\n\n", payload)

				flusher.Flush()
				return
			}
		}
	}
}
