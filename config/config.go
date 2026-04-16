package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the top-level benchmarkr configuration.
type Config struct {
	Storage StorageConfig `toml:"storage"`
	Cloud   CloudConfig   `toml:"cloud"`
}

// StorageConfig controls where benchmark results are persisted.
type StorageConfig struct {
	Mode  string             `toml:"mode"` // "local" or "cloud"
	Local LocalStorageConfig `toml:"local"`
}

// LocalStorageConfig holds settings for local storage backends.
type LocalStorageConfig struct {
	Driver   string               `toml:"driver"` // "json", "postgres", "mysql"
	JSON     JSONDriverConfig     `toml:"json"`
	Postgres PostgresDriverConfig `toml:"postgres"`
	MySQL    MySQLDriverConfig    `toml:"mysql"`
}

// JSONDriverConfig holds settings for the JSON file backend.
type JSONDriverConfig struct {
	OutputDir string `toml:"output_dir"`
}

// PostgresDriverConfig holds settings for the PostgreSQL backend.
type PostgresDriverConfig struct {
	Host        string `toml:"host"`
	Port        int    `toml:"port"`
	Database    string `toml:"database"`
	User        string `toml:"user"`
	PasswordEnv string `toml:"password_env"`
	SSL         bool   `toml:"ssl"`
}

// MySQLDriverConfig holds settings for the MySQL backend.
type MySQLDriverConfig struct {
	Host        string `toml:"host"`
	Port        int    `toml:"port"`
	Database    string `toml:"database"`
	User        string `toml:"user"`
	PasswordEnv string `toml:"password_env"`
}

// CloudConfig holds settings for the cloud API backend.
type CloudConfig struct {
	API_URL   string `toml:"api_url"`
	TokenEnv string `toml:"token_env"`
}

// Source describes where a loaded config came from.
type Source struct {
	Path   string // file path that was loaded
	Origin string // "env", "project", or "global"
}

// Defaults returns a Config populated with sensible default values.
func Defaults() *Config {
	return &Config{
		Storage: StorageConfig{
			Mode: "local",
			Local: LocalStorageConfig{
				Driver: "json",
				JSON: JSONDriverConfig{
					OutputDir: "~/.benchmarkr/runs",
				},
				Postgres: PostgresDriverConfig{
					Host:        "localhost",
					Port:        5432,
					Database:    "benchmarks",
					User:        "bench",
					PasswordEnv: "BENCH_PG_PASS",
				},
				MySQL: MySQLDriverConfig{
					Host:        "localhost",
					Port:        3306,
					Database:    "benchmarks",
					User:        "bench",
					PasswordEnv: "BENCH_MYSQL_PASS",
				},
			},
		},
		Cloud: CloudConfig{
			API_URL:   "https://api.yourdomain.com",
			TokenEnv: "BENCH_CLOUD_TOKEN",
		},
	}
}

// DefaultGlobalPath returns the platform-specific global config file path.
func DefaultGlobalPath() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appData, "benchmarkr", "config.toml")
	default: // darwin, linux
		return filepath.Join(home, ".config", "benchmarkr", "config.toml")
	}
}

// Load resolves and loads the config following precedence:
//  1. BENCH_CONFIG env var
//  2. .benchrc.toml in CWD (project-level, merged over global)
//  3. Global config path (platform-specific)
func Load() (*Config, *Source, error) {
	return LoadWithGlobalPath(DefaultGlobalPath())
}

// LoadWithGlobalPath is like Load but uses the given path for the global config
// instead of the platform default. This is useful for testing.
func LoadWithGlobalPath(globalPath string) (*Config, *Source, error) {
	// 1. Env var override
	if p := os.Getenv("BENCH_CONFIG"); p != "" {
		cfg, err := LoadFromPath(p)
		if err != nil {
			return nil, nil, fmt.Errorf("loading BENCH_CONFIG=%s: %w", p, err)
		}
		return cfg, &Source{Path: p, Origin: "env"}, nil
	}

	// 2. Project-level .benchrc.toml (merged over global)
	if cwd, err := os.Getwd(); err == nil {
		projectPath := filepath.Join(cwd, ".benchrc.toml")
		if _, err := os.Stat(projectPath); err == nil {
			// Start with global as base if it exists
			cfg := &Config{}
			if _, statErr := os.Stat(globalPath); statErr == nil {
				if base, loadErr := LoadFromPath(globalPath); loadErr == nil {
					cfg = base
				}
			}
			// Overlay project-level values (only sets fields present in the file)
			if _, err := toml.DecodeFile(projectPath, cfg); err != nil {
				return nil, nil, fmt.Errorf("parsing project config %s: %w", projectPath, err)
			}
			return cfg, &Source{Path: projectPath, Origin: "project"}, nil
		}
	}

	// 3. Global config
	if _, err := os.Stat(globalPath); err != nil {
		return nil, nil, fmt.Errorf("no config file found (checked %s)\nRun 'benchmarkr config init' to create one", globalPath)
	}
	cfg, err := LoadFromPath(globalPath)
	if err != nil {
		return nil, nil, err
	}
	return cfg, &Source{Path: globalPath, Origin: "global"}, nil
}

// LoadFromPath decodes a TOML config file at the given path.
func LoadFromPath(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &cfg, nil
}

// Save writes the config to path, creating parent directories as needed.
func Save(cfg *Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("# benchmarkr configuration\n# See: benchmarkr config show\n\n")
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// ExpandPath expands a leading ~ to the user's home directory.
func ExpandPath(path string) string {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return home
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

// IsStorageConfigured returns true if the config has a usable storage mode set.
func (c *Config) IsStorageConfigured() bool {
	switch c.Storage.Mode {
	case "cloud":
		return c.Cloud.API_URL != ""
	case "local":
		return c.Storage.Local.Driver != ""
	default:
		return false
	}
}

// Get retrieves a config value by dotted key path.
func (c *Config) Get(key string) (string, error) {
	switch key {
	case "storage.mode":
		return c.Storage.Mode, nil
	case "storage.local.driver":
		return c.Storage.Local.Driver, nil
	case "storage.local.json.output_dir":
		return c.Storage.Local.JSON.OutputDir, nil
	case "storage.local.postgres.host":
		return c.Storage.Local.Postgres.Host, nil
	case "storage.local.postgres.port":
		return fmt.Sprintf("%d", c.Storage.Local.Postgres.Port), nil
	case "storage.local.postgres.database":
		return c.Storage.Local.Postgres.Database, nil
	case "storage.local.postgres.user":
		return c.Storage.Local.Postgres.User, nil
	case "storage.local.postgres.password_env":
		return c.Storage.Local.Postgres.PasswordEnv, nil
	case "storage.local.postgres.ssl":
		return fmt.Sprintf("%t", c.Storage.Local.Postgres.SSL), nil
	case "storage.local.mysql.host":
		return c.Storage.Local.MySQL.Host, nil
	case "storage.local.mysql.port":
		return fmt.Sprintf("%d", c.Storage.Local.MySQL.Port), nil
	case "storage.local.mysql.database":
		return c.Storage.Local.MySQL.Database, nil
	case "storage.local.mysql.user":
		return c.Storage.Local.MySQL.User, nil
	case "storage.local.mysql.password_env":
		return c.Storage.Local.MySQL.PasswordEnv, nil
	case "cloud.api_url":
		return c.Cloud.API_URL, nil
	case "cloud.token_env":
		return c.Cloud.TokenEnv, nil
	default:
		return "", fmt.Errorf("unknown config key: %s", key)
	}
}

// Set updates a config value by dotted key path with validation.
func (c *Config) Set(key, value string) error {
	switch key {
	case "storage.mode":
		if value != "local" && value != "cloud" {
			return fmt.Errorf("storage.mode must be 'local' or 'cloud', got %q", value)
		}
		c.Storage.Mode = value
	case "storage.local.driver":
		if value != "json" && value != "postgres" && value != "mysql" {
			return fmt.Errorf("storage.local.driver must be 'json', 'postgres', or 'mysql', got %q", value)
		}
		c.Storage.Local.Driver = value
	case "storage.local.json.output_dir":
		c.Storage.Local.JSON.OutputDir = value
	case "storage.local.postgres.host":
		c.Storage.Local.Postgres.Host = value
	case "storage.local.postgres.port":
		var port int
		if _, err := fmt.Sscanf(value, "%d", &port); err != nil {
			return fmt.Errorf("invalid port number: %s", value)
		}
		c.Storage.Local.Postgres.Port = port
	case "storage.local.postgres.database":
		c.Storage.Local.Postgres.Database = value
	case "storage.local.postgres.user":
		c.Storage.Local.Postgres.User = value
	case "storage.local.postgres.password_env":
		c.Storage.Local.Postgres.PasswordEnv = value
	case "storage.local.postgres.ssl":
		c.Storage.Local.Postgres.SSL = value == "true"
	case "storage.local.mysql.host":
		c.Storage.Local.MySQL.Host = value
	case "storage.local.mysql.port":
		var port int
		if _, err := fmt.Sscanf(value, "%d", &port); err != nil {
			return fmt.Errorf("invalid port number: %s", value)
		}
		c.Storage.Local.MySQL.Port = port
	case "storage.local.mysql.database":
		c.Storage.Local.MySQL.Database = value
	case "storage.local.mysql.user":
		c.Storage.Local.MySQL.User = value
	case "storage.local.mysql.password_env":
		c.Storage.Local.MySQL.PasswordEnv = value
	case "cloud.api_url":
		c.Cloud.API_URL = value
	case "cloud.token_env":
		c.Cloud.TokenEnv = value
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}
	return nil
}
