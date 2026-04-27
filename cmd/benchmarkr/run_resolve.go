package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mack-Overflow/api-bench/benchmark"
	"github.com/Mack-Overflow/api-bench/config"
	"github.com/Mack-Overflow/api-bench/db"
)

type buildOpts struct {
	URL             string
	Method          string
	Headers         []string
	Params          []string
	Body            string
	Concurrency     int
	Duration        int
	RateLimit       int
	Throttle        int
	CacheMode       string
	Name            string
	EndpointName    string
	EndpointVersion int
	EndpointFile    string
	Store           bool

	// SetFlags lists the long-form names of flags explicitly passed by the user.
	SetFlags map[string]bool
}

// buildRequestFromFlags is the URL-driven path (no local config involved).
func buildRequestFromFlags(opts buildOpts) (benchmark.StartBenchmarkRequest, error) {
	headersJSON, err := encodeHeaders(opts.Headers)
	if err != nil {
		return benchmark.StartBenchmarkRequest{}, err
	}
	paramsJSON, err := encodeParams(opts.Params)
	if err != nil {
		return benchmark.StartBenchmarkRequest{}, err
	}
	bodyJSON, err := encodeBody(opts.Body)
	if err != nil {
		return benchmark.StartBenchmarkRequest{}, err
	}

	name := opts.Name
	if name == "" {
		name = opts.URL
	}

	return benchmark.StartBenchmarkRequest{
		Name:           name,
		URL:            opts.URL,
		Method:         strings.ToUpper(opts.Method),
		Headers:        headersJSON,
		Params:         paramsJSON,
		Body:           bodyJSON,
		Concurrency:    opts.Concurrency,
		RateLimit:      opts.RateLimit,
		DurationSec:    opts.Duration,
		ThrottleTimeMs: opts.Throttle,
		CacheMode:      benchmark.CacheMode(opts.CacheMode),
	}, nil
}

// buildRequestFromEndpoint resolves a saved endpoint from the local config,
// optionally fetching a pinned version from the cloud, and applies CLI flag
// overrides on top of the endpoint's defaults.
func buildRequestFromEndpoint(opts buildOpts) (benchmark.StartBenchmarkRequest, error) {
	resolvedPath, err := resolveEndpointFilePath(opts.EndpointFile, true)
	if err != nil {
		return benchmark.StartBenchmarkRequest{}, err
	}
	file, err := config.LoadEndpointFile(resolvedPath)
	if err != nil {
		return benchmark.StartBenchmarkRequest{}, err
	}
	endpoint, err := config.FindEndpointByName(file, opts.EndpointName)
	if err != nil {
		return benchmark.StartBenchmarkRequest{}, err
	}

	config.LoadDotenvForFile(resolvedPath)
	if err := config.ExpandEndpoint(endpoint); err != nil {
		return benchmark.StartBenchmarkRequest{}, err
	}

	// -v flag takes precedence over the endpoint's pinned version field.
	pinnedVersion := opts.EndpointVersion
	if pinnedVersion == 0 && endpoint.Version != nil {
		pinnedVersion = *endpoint.Version
	}

	method := strings.ToUpper(endpoint.Method)
	url := endpoint.URL
	headersJSON, err := mapToJSON(endpoint.Headers)
	if err != nil {
		return benchmark.StartBenchmarkRequest{}, err
	}
	paramsJSON, err := mapToJSON(endpoint.Params)
	if err != nil {
		return benchmark.StartBenchmarkRequest{}, err
	}
	bodyJSON, err := bodyToJSON(endpoint.Body)
	if err != nil {
		return benchmark.StartBenchmarkRequest{}, err
	}

	var (
		endpointVersionID *int64
		userID            *int64
	)

	if pinnedVersion != 0 {
		// Best-effort: fetch the pinned version from cloud and replace the
		// request config with its stored values. When cloud is not configured
		// (no DB_URL, no API key, etc.) the pin acts as metadata only and we
		// fall back to the local config.
		if uID, store, closeFn, err := openCloudWithUserID(); err == nil {
			defer closeFn()

			cloudEndpoint, err := store.GetEndpointByName(&uID, endpoint.Name)
			switch {
			case err == nil:
				version, err := store.GetEndpointVersion(cloudEndpoint.ID, pinnedVersion)
				if err == nil {
					method = strings.ToUpper(version.Method)
					url = version.URL
					if len(version.Headers) > 0 {
						headersJSON = version.Headers
					}
					if len(version.Params) > 0 {
						paramsJSON = version.Params
					}
					if len(version.Body) > 0 {
						bodyJSON = version.Body
					}
					endpointVersionID = &version.ID
					userID = &uID
				} else if !errors.Is(err, sql.ErrNoRows) {
					return benchmark.StartBenchmarkRequest{}, fmt.Errorf("lookup endpoint version: %w", err)
				}
				// sql.ErrNoRows: cloud endpoint exists but not at this version — fall back to local.
			case errors.Is(err, sql.ErrNoRows):
				// Cloud endpoint not found — fall back to local config.
			default:
				return benchmark.StartBenchmarkRequest{}, fmt.Errorf("lookup endpoint: %w", err)
			}
		}
	} else if opts.Store {
		// Best-effort user_id resolution so the persist step can scope the
		// endpoint upsert. Local-config runs without a token still work —
		// they just persist anonymously.
		if uID, err := tryResolveUserID(); err == nil {
			userID = &uID
		}
	}

	// Apply endpoint defaults, then override with explicitly-set CLI flags.
	concurrency := opts.Concurrency
	duration := opts.Duration
	rateLimit := opts.RateLimit
	throttle := opts.Throttle
	cacheMode := opts.CacheMode

	if endpoint.Defaults != nil {
		d := endpoint.Defaults
		if !opts.SetFlags["concurrency"] && d.Concurrency > 0 {
			concurrency = d.Concurrency
		}
		if !opts.SetFlags["duration"] && d.DurationSec > 0 {
			duration = d.DurationSec
		}
		if !opts.SetFlags["rate-limit"] && d.RateLimit > 0 {
			rateLimit = d.RateLimit
		}
		if !opts.SetFlags["throttle"] && d.ThrottleTimeMs > 0 {
			throttle = d.ThrottleTimeMs
		}
		if !opts.SetFlags["cache-mode"] && d.CacheMode != "" {
			cacheMode = d.CacheMode
		}
	}

	// CLI overrides for headers/params/body merge on top of the endpoint config.
	if len(opts.Headers) > 0 {
		extra, err := encodeHeaders(opts.Headers)
		if err != nil {
			return benchmark.StartBenchmarkRequest{}, err
		}
		headersJSON = mergeJSONMaps(headersJSON, extra)
	}
	if len(opts.Params) > 0 {
		extra, err := encodeParams(opts.Params)
		if err != nil {
			return benchmark.StartBenchmarkRequest{}, err
		}
		paramsJSON = mergeJSONMaps(paramsJSON, extra)
	}
	if opts.SetFlags["body"] {
		extra, err := encodeBody(opts.Body)
		if err != nil {
			return benchmark.StartBenchmarkRequest{}, err
		}
		bodyJSON = extra
	}
	if opts.SetFlags["method"] {
		method = strings.ToUpper(opts.Method)
	}

	name := opts.Name
	if name == "" {
		name = endpoint.Name
	}

	req := benchmark.StartBenchmarkRequest{
		Name:              name,
		URL:               url,
		Method:            method,
		Headers:           headersJSON,
		Params:            paramsJSON,
		Body:              bodyJSON,
		Concurrency:       concurrency,
		RateLimit:         rateLimit,
		DurationSec:       duration,
		ThrottleTimeMs:    throttle,
		CacheMode:         benchmark.CacheMode(cacheMode),
		EndpointVersionID: endpointVersionID,
		UserID:            userID,
	}
	return req, nil
}

// openCloudWithUserID opens a DB connection from DB_URL and resolves the
// caller's user_id from BENCH_CLOUD_TOKEN (or the configured token env var).
// Returns the user_id, a *db.DB handle, and a close function.
func openCloudWithUserID() (int64, *db.DB, func(), error) {
	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		return 0, nil, nil, fmt.Errorf("DB_URL is required")
	}

	apiKey, err := lookupAPIKey()
	if err != nil {
		return 0, nil, nil, err
	}

	sqlDB, err := db.OpenDB(dbURL)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("connect to database: %w", err)
	}
	store := db.New(sqlDB)
	closeFn := func() { sqlDB.Close() }

	userID, err := store.GetUserIDByAPIKey(apiKey)
	if err != nil {
		closeFn()
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil, nil, fmt.Errorf("api key not recognized")
		}
		return 0, nil, nil, fmt.Errorf("lookup api key: %w", err)
	}
	return userID, store, closeFn, nil
}

// tryResolveUserID is a best-effort version of openCloudWithUserID that
// closes the DB before returning. Used when we want to attribute a run to a
// user but don't otherwise need the cloud connection at request-build time.
func tryResolveUserID() (int64, error) {
	userID, _, closeFn, err := openCloudWithUserID()
	if err != nil {
		return 0, err
	}
	closeFn()
	return userID, nil
}

// lookupAPIKey returns the user's API key by reading the env var named in
// the cloud config (defaults to BENCH_CLOUD_TOKEN).
func lookupAPIKey() (string, error) {
	tokenEnv := "BENCH_CLOUD_TOKEN"
	if cfg, _, err := config.Load(); err == nil && cfg.Cloud.TokenEnv != "" {
		tokenEnv = cfg.Cloud.TokenEnv
	}
	apiKey := os.Getenv(tokenEnv)
	if apiKey == "" {
		return "", fmt.Errorf("api key env var %s is not set", tokenEnv)
	}
	return apiKey, nil
}

// resolveEndpointFilePath is also defined in endpoints.go; this duplicate is
// kept package-private and serves the run command. (Both files are in the
// same package, so the function is shared — this comment is just a reminder
// not to reintroduce two copies.)

// flagsExplicitlySet returns the long-form names of all flags that were
// passed by the user (as opposed to taking their default value).
func flagsExplicitlySet(fs *flag.FlagSet) map[string]bool {
	set := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		set[f.Name] = true
	})
	return set
}

// --- header / param / body helpers ---

func encodeHeaders(headers []string) (json.RawMessage, error) {
	m := make(map[string]string)
	for _, h := range headers {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid header format %q (expected \"Key: Value\")", h)
		}
		m[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	out, _ := json.Marshal(m)
	return out, nil
}

func encodeParams(params []string) (json.RawMessage, error) {
	m := make(map[string]string)
	for _, p := range params {
		parts := strings.SplitN(p, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid param format %q (expected \"key=value\")", p)
		}
		m[parts[0]] = parts[1]
	}
	out, _ := json.Marshal(m)
	return out, nil
}

func encodeBody(body string) (json.RawMessage, error) {
	if body == "" {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid([]byte(body)) {
		return nil, fmt.Errorf("--body must be valid JSON")
	}
	return json.RawMessage(body), nil
}

func mapToJSON(m map[string]string) (json.RawMessage, error) {
	if m == nil {
		m = make(map[string]string)
	}
	out, err := json.Marshal(m)
	return out, err
}

func bodyToJSON(body interface{}) (json.RawMessage, error) {
	if body == nil {
		return json.RawMessage(`{}`), nil
	}
	out, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}
	return out, nil
}

// mergeJSONMaps overlays b onto a (both expected to be JSON objects of
// string→string). Unknown shapes fall back to b.
func mergeJSONMaps(a, b json.RawMessage) json.RawMessage {
	if len(a) == 0 {
		return b
	}
	var am, bm map[string]string
	if err := json.Unmarshal(a, &am); err != nil {
		return b
	}
	if err := json.Unmarshal(b, &bm); err != nil {
		return a
	}
	for k, v := range bm {
		am[k] = v
	}
	out, _ := json.Marshal(am)
	return out
}

// --- file-discovery helper used by both run and endpoints commands ---

var _ = filepath.Separator // keep filepath import in case future helpers need it
