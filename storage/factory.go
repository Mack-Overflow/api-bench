package storage

import (
	"fmt"

	"github.com/Mack-Overflow/api-bench/config"
)

// NewBackendFromConfig reads the config and returns the appropriate StorageBackend.
// This is the only place driver selection happens.
func NewBackendFromConfig(cfg *config.Config) (StorageBackend, error) {
	switch cfg.Storage.Mode {
	case "local":
		return newLocalBackend(cfg)
	case "cloud":
		return nil, fmt.Errorf("cloud storage uses direct database access; configure local storage or use DB_URL")
	default:
		return nil, fmt.Errorf("unknown storage mode: %q", cfg.Storage.Mode)
	}
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
