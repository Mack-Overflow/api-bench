package storage

import (
	"fmt"
	"os"

	"github.com/Mack-Overflow/api-bench/config"
)

// NewBackendFromConfig reads the config and returns the appropriate StorageBackend.
// This is the only place driver selection happens.
func NewBackendFromConfig(cfg *config.Config) (StorageBackend, error) {
	switch cfg.Storage.Mode {
	case "local":
		return newLocalBackend(cfg)
	case "cloud":
		return newCloudBackendFromConfig(cfg)
	default:
		return nil, fmt.Errorf("unknown storage mode: %q", cfg.Storage.Mode)
	}
}

// ResolveCloudToken returns the bearer token to send to the Laravel API. It
// prefers an inline cfg.Cloud.Token (set via `benchmarkr config set cloud.token`),
// falling back to the env var named by cfg.Cloud.TokenEnv (default
// BENCH_CLOUD_TOKEN). Returns an error if neither is set.
func ResolveCloudToken(cfg *config.Config) (string, error) {
	if cfg.Cloud.Token != "" {
		return cfg.Cloud.Token, nil
	}
	tokenEnv := cfg.Cloud.TokenEnv
	if tokenEnv == "" {
		tokenEnv = "BENCH_CLOUD_TOKEN"
	}
	token := os.Getenv(tokenEnv)
	if token == "" {
		return "", fmt.Errorf("no cloud api token: set 'cloud.token' in config or export $%s", tokenEnv)
	}
	return token, nil
}

func newCloudBackendFromConfig(cfg *config.Config) (StorageBackend, error) {
	token, err := ResolveCloudToken(cfg)
	if err != nil {
		return nil, err
	}
	return NewCloudBackend(cfg.Cloud.API_URL, token)
}

func newLocalBackend(cfg *config.Config) (StorageBackend, error) {
	switch cfg.Storage.Local.Driver {
	case "json":
		dir := config.ExpandPath(cfg.Storage.Local.JSON.OutputDir)
		return NewJSONBackend(dir), nil
	case "postgres":
		return NewPostgresBackend(cfg.Storage.Local.Postgres)
	case "mysql":
		return NewMySQLBackend(cfg.Storage.Local.MySQL)
	default:
		return nil, fmt.Errorf("unknown storage driver: %q", cfg.Storage.Local.Driver)
	}
}
