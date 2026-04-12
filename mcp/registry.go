package mcp

import (
	"fmt"
	"sync"

	"github.com/Mack-Overflow/api-bench/benchmark"
)

// RunRegistry tracks active benchmark runs within the MCP server process.
type RunRegistry struct {
	mu   sync.RWMutex
	runs map[string]*benchmark.ActiveRun
}

func NewRunRegistry() *RunRegistry {
	return &RunRegistry{
		runs: make(map[string]*benchmark.ActiveRun),
	}
}

func (r *RunRegistry) Register(id string, run *benchmark.ActiveRun) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs[id] = run
}

func (r *RunRegistry) Get(id string) (*benchmark.ActiveRun, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	run, ok := r.runs[id]
	if !ok {
		return nil, fmt.Errorf("benchmark run %q not found", id)
	}
	return run, nil
}

func (r *RunRegistry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.runs, id)
}
