package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Mack-Overflow/api-bench/config"
	"github.com/Mack-Overflow/api-bench/storage"
)

const (
	settingsURL = "https://benchmarkr-1.onrender.com/settings"
	billingURL  = "https://benchmarkr-1.onrender.com/settings#billing"
)

// cloudPreflightForCLI runs the preflight check when cloud storage is the
// configured backend. Returns disableStorage=true when the user has hit
// their storage cap (run proceeds but is not persisted). Returns an error
// when the API key is invalid — the caller aborts.
func cloudPreflightForCLI(cfg *config.Config) (disableStorage bool, err error) {
	if cfg.Storage.Mode != "cloud" {
		return false, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pf, err := storage.CloudPreflight(ctx, cfg, storage.PreflightOpts{})
	switch {
	case errors.Is(err, storage.ErrAuth):
		return false, errors.New(apiKeyRemediation())
	case err != nil:
		return false, fmt.Errorf("preflight: %w", err)
	case !pf.Storage.Allowed:
		fmt.Fprintln(os.Stderr, storageRemediation(pf.Storage))
		return true, nil
	}
	return false, nil
}

func apiKeyRemediation() string {
	return fmt.Sprintf(`your API key is missing or invalid.
  1. Visit %s to generate a new one.
  2. Run: benchmarkr config set cloud.token <KEY>`, settingsURL)
}

func storageRemediation(s storage.StorageCheck) string {
	return fmt.Sprintf(`You've reached your stored-runs limit (%d/%d). The benchmark will run, but results won't be saved.
Upgrade your plan at %s`, s.Stored, s.Limit, billingURL)
}
