package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// JSONBackend stores benchmark runs as individual JSON files in a directory,
// with a lightweight index.json for fast listing.
type JSONBackend struct {
	dir string
	mu  sync.Mutex // protects index reads/writes within a process
}

// NewJSONBackend creates a JSONBackend that stores files under dir.
// The directory is created on the first SaveRun call.
func NewJSONBackend(dir string) *JSONBackend {
	return &JSONBackend{dir: dir}
}

// indexEntry extends RunSummary with the on-disk filename.
type indexEntry struct {
	RunSummary
	File string `json:"file"`
}

// --- StorageBackend implementation ---

func (b *JSONBackend) SaveRun(_ context.Context, run BenchmarkRun) error {
	if err := os.MkdirAll(b.dir, 0755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	filename := buildFilename(run)
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling run: %w", err)
	}

	runPath := filepath.Join(b.dir, filename)
	if err := atomicWrite(runPath, data); err != nil {
		return fmt.Errorf("writing run file: %w", err)
	}

	// Update index
	b.mu.Lock()
	defer b.mu.Unlock()

	entries, _ := b.readIndex() // ignore error; start fresh if corrupt
	entries = append(entries, indexEntry{
		RunSummary: SummaryFromRun(run),
		File:       filename,
	})
	// Keep sorted newest-first
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].StartedAt.After(entries[j].StartedAt)
	})
	return b.writeIndex(entries)
}

func (b *JSONBackend) ListRuns(_ context.Context, filter RunFilter) ([]RunSummary, error) {
	b.mu.Lock()
	entries, err := b.readIndex()
	b.mu.Unlock()

	if err != nil {
		return nil, err
	}

	var results []RunSummary
	for _, e := range entries {
		if !matchesFilter(e.RunSummary, filter) {
			continue
		}
		results = append(results, e.RunSummary)
		if filter.Limit > 0 && len(results) >= filter.Limit {
			break
		}
	}
	return results, nil
}

func (b *JSONBackend) GetRun(_ context.Context, id string) (BenchmarkRun, error) {
	// Try index lookup first
	b.mu.Lock()
	entries, _ := b.readIndex()
	b.mu.Unlock()

	for _, e := range entries {
		if e.ID == id {
			path := filepath.Join(b.dir, e.File)
			return readRunFile(path)
		}
	}

	// Fallback: glob for files matching the short ID
	shortID := id
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	matches, _ := filepath.Glob(filepath.Join(b.dir, "*_"+shortID+".json"))
	for _, path := range matches {
		run, err := readRunFile(path)
		if err != nil {
			continue
		}
		if run.ID == id {
			return run, nil
		}
	}

	return BenchmarkRun{}, fmt.Errorf("run %s not found", id)
}

func (b *JSONBackend) DeleteRun(_ context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	entries, _ := b.readIndex()

	found := false
	remaining := make([]indexEntry, 0, len(entries))
	for _, e := range entries {
		if e.ID == id {
			found = true
			path := filepath.Join(b.dir, e.File)
			os.Remove(path) // best-effort file removal
			continue
		}
		remaining = append(remaining, e)
	}

	if !found {
		return fmt.Errorf("run %s not found", id)
	}

	return b.writeIndex(remaining)
}

// --- index helpers ---

func (b *JSONBackend) readIndex() ([]indexEntry, error) {
	data, err := os.ReadFile(filepath.Join(b.dir, "index.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var entries []indexEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		// Index is corrupt — rebuild from run files on disk
		rebuilt, rebuildErr := b.rebuildFromFiles()
		if rebuildErr != nil {
			return nil, fmt.Errorf("index corrupt and rebuild failed: %w", rebuildErr)
		}
		b.writeIndex(rebuilt) // best-effort rewrite
		return rebuilt, nil
	}
	return entries, nil
}

func (b *JSONBackend) writeIndex(entries []indexEntry) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling index: %w", err)
	}
	return atomicWrite(filepath.Join(b.dir, "index.json"), data)
}

func (b *JSONBackend) rebuildFromFiles() ([]indexEntry, error) {
	matches, err := filepath.Glob(filepath.Join(b.dir, "*.json"))
	if err != nil {
		return nil, err
	}

	var entries []indexEntry
	for _, path := range matches {
		if filepath.Base(path) == "index.json" {
			continue
		}
		run, err := readRunFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping corrupt file %s: %v\n", filepath.Base(path), err)
			continue
		}
		entries = append(entries, indexEntry{
			RunSummary: SummaryFromRun(run),
			File:       filepath.Base(path),
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].StartedAt.After(entries[j].StartedAt)
	})
	return entries, nil
}

// --- file helpers ---

func readRunFile(path string) (BenchmarkRun, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BenchmarkRun{}, err
	}
	var run BenchmarkRun
	if err := json.Unmarshal(data, &run); err != nil {
		return BenchmarkRun{}, fmt.Errorf("parsing %s: %w", filepath.Base(path), err)
	}
	return run, nil
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// buildFilename returns {slug}_{timestamp}_{short_id}.json
func buildFilename(run BenchmarkRun) string {
	slug := slugify(run.URL)
	ts := run.StartedAt.UTC().Format("20060102T150405Z")
	shortID := run.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	return fmt.Sprintf("%s_%s_%s.json", slug, ts, shortID)
}

// slugify converts a URL into a filesystem-safe slug.
func slugify(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "unknown"
	}

	// Combine hostname (without port) and path
	raw := u.Hostname() + u.Path

	var b strings.Builder
	for _, r := range raw {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}

	slug := strings.ToLower(b.String())
	// Collapse repeated hyphens
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")

	if len(slug) > 80 {
		slug = slug[:80]
	}
	if slug == "" {
		slug = "unknown"
	}
	return slug
}

// matchesFilter checks whether a summary passes the given filter criteria.
func matchesFilter(s RunSummary, f RunFilter) bool {
	if f.Endpoint != "" && !strings.Contains(s.URL, f.Endpoint) {
		return false
	}
	if f.Since != nil && s.StartedAt.Before(*f.Since) {
		return false
	}
	if f.Before != nil && !s.StartedAt.Before(*f.Before) {
		return false
	}
	return true
}
