package benchmark

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// ActiveRun is a handle to a running or completed benchmark.
type ActiveRun struct {
	mu sync.RWMutex

	Metrics    *BenchmarkMetrics
	status     BenchmarkStatus
	stopReason StopReason
	result     *BenchmarkResult
	StartedAt  time.Time
	endedAt    *time.Time
	cancel     context.CancelFunc
	done       chan struct{}
}

func (r *ActiveRun) Cancel() {
	r.cancel()
}

func (r *ActiveRun) Done() <-chan struct{} {
	return r.done
}

// Wait blocks until the benchmark completes and returns the result.
func (r *ActiveRun) Wait() (*BenchmarkResult, StopReason) {
	<-r.done
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.result, r.stopReason
}

func (r *ActiveRun) GetStatus() BenchmarkStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

func (r *ActiveRun) GetResult() *BenchmarkResult {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.result
}

func (r *ActiveRun) GetStopReason() StopReason {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.stopReason
}

func (r *ActiveRun) GetEndedAt() *time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.endedAt
}

// SnapshotLogs returns current metrics and any logs added since the given cursor.
func (r *ActiveRun) SnapshotLogs(cursor int) (MetricsSnapshot, int) {
	return r.Metrics.SnapshotLogs(cursor)
}

// Start launches a benchmark asynchronously and returns a handle.
func Start(req StartBenchmarkRequest) *ActiveRun {
	ctx, cancel := context.WithCancel(context.Background())

	run := &ActiveRun{
		Metrics:   &BenchmarkMetrics{},
		status:    StatusPending,
		StartedAt: time.Now(),
		cancel:    cancel,
		done:      make(chan struct{}),
	}

	go run.execute(ctx, req)

	return run
}

func (r *ActiveRun) execute(ctx context.Context, req StartBenchmarkRequest) {
	defer close(r.done)

	r.mu.Lock()
	r.status = StatusRunning
	r.mu.Unlock()

	const maxConsecutiveErrors = 3
	errorTracker := newErrorTracker(maxConsecutiveErrors, r.cancel, r.Metrics)

	timer := time.NewTimer(time.Duration(req.DurationSec) * time.Second)
	defer timer.Stop()

	if req.CacheMode == CacheWarm {
		log.Printf("warming cache for %s", req.URL)
		WarmCache(req)
	}

	r.Metrics.AddLog("info", fmt.Sprintf("benchmark started: %d workers, %ds duration", req.Concurrency, req.DurationSec))

	limiter := newRateLimiter(req.RateLimit)
	throttle := time.Duration(req.ThrottleTimeMs) * time.Millisecond

	var wg sync.WaitGroup
	for i := 0; i < req.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			runWorker(ctx, workerID, req, r.Metrics, errorTracker, limiter, throttle)
		}(i)
	}

	var stopReason StopReason
	select {
	case <-timer.C:
		stopReason = StopCompleted
	case <-ctx.Done():
		stopReason = StopErrors
	}

	r.cancel()
	wg.Wait()

	r.Metrics.AddLog("info", fmt.Sprintf("benchmark finished: %s", stopReason))

	end := time.Now()
	result := r.Metrics.ComputeResult()

	r.mu.Lock()
	r.status = StatusCompleted
	r.stopReason = stopReason
	r.result = result
	r.endedAt = &end
	r.mu.Unlock()
}
