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

func newCloudBackendFromConfig(cfg *config.Config) (StorageBackend, error) {
	tokenEnv := cfg.Cloud.TokenEnv
	if tokenEnv == "" {
		tokenEnv = "BENCH_CLOUD_TOKEN"
	}
	token := os.Getenv(tokenEnv)
	if token == "" {
		return nil, fmt.Errorf("cloud api token env var $%s is not set", tokenEnv)
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
