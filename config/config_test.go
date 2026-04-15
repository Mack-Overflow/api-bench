package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	want := &Config{
		Storage: StorageConfig{
			Mode: "local",
			Local: LocalStorageConfig{
				Driver: "json",
				JSON: JSONDriverConfig{
					OutputDir: "/tmp/bench-runs",
				},
				Postgres: PostgresDriverConfig{
					Host:        "db.example.com",
					Port:        5433,
					Database:    "mydb",
					User:        "admin",
					PasswordEnv: "MY_PG_PASS",
					SSL:         true,
				},
				MySQL: MySQLDriverConfig{
					Host:        "mysql.example.com",
					Port:        3307,
					Database:    "mydb2",
					User:        "root",
					PasswordEnv: "MY_MYSQL_PASS",
				},
			},
		},
		Cloud: CloudConfig{
			APIURL:   "https://bench.example.com/api",
			TokenEnv: "MY_TOKEN",
		},
	}

	if err := Save(want, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := LoadFromPath(path)
	if err != nil {
		t.Fatalf("LoadFromPath: %v", err)
	}

	// Assert every field
	assertStr(t, "storage.mode", got.Storage.Mode, want.Storage.Mode)
	assertStr(t, "storage.local.driver", got.Storage.Local.Driver, want.Storage.Local.Driver)
	assertStr(t, "storage.local.json.output_dir", got.Storage.Local.JSON.OutputDir, want.Storage.Local.JSON.OutputDir)
	assertStr(t, "storage.local.postgres.host", got.Storage.Local.Postgres.Host, want.Storage.Local.Postgres.Host)
	assertInt(t, "storage.local.postgres.port", got.Storage.Local.Postgres.Port, want.Storage.Local.Postgres.Port)
	assertStr(t, "storage.local.postgres.database", got.Storage.Local.Postgres.Database, want.Storage.Local.Postgres.Database)
	assertStr(t, "storage.local.postgres.user", got.Storage.Local.Postgres.User, want.Storage.Local.Postgres.User)
	assertStr(t, "storage.local.postgres.password_env", got.Storage.Local.Postgres.PasswordEnv, want.Storage.Local.Postgres.PasswordEnv)
	assertBool(t, "storage.local.postgres.ssl", got.Storage.Local.Postgres.SSL, want.Storage.Local.Postgres.SSL)
	assertStr(t, "storage.local.mysql.host", got.Storage.Local.MySQL.Host, want.Storage.Local.MySQL.Host)
	assertInt(t, "storage.local.mysql.port", got.Storage.Local.MySQL.Port, want.Storage.Local.MySQL.Port)
	assertStr(t, "storage.local.mysql.database", got.Storage.Local.MySQL.Database, want.Storage.Local.MySQL.Database)
	assertStr(t, "storage.local.mysql.user", got.Storage.Local.MySQL.User, want.Storage.Local.MySQL.User)
	assertStr(t, "storage.local.mysql.password_env", got.Storage.Local.MySQL.PasswordEnv, want.Storage.Local.MySQL.PasswordEnv)
	assertStr(t, "cloud.api_url", got.Cloud.APIURL, want.Cloud.APIURL)
	assertStr(t, "cloud.token_env", got.Cloud.TokenEnv, want.Cloud.TokenEnv)
}

func TestResolutionOrder(t *testing.T) {
	globalDir := t.TempDir()
	projectDir := t.TempDir()
	envDir := t.TempDir()

	globalPath := filepath.Join(globalDir, "config.toml")
	projectPath := filepath.Join(projectDir, ".benchrc.toml")
	envPath := filepath.Join(envDir, "env-config.toml")

	// Create configs with distinct values
	globalCfg := Defaults()
	globalCfg.Storage.Mode = "cloud"
	globalCfg.Cloud.APIURL = "https://global.example.com"
	Save(globalCfg, globalPath)

	// Project config only overrides storage.mode — cloud.api_url should come from global.
	// Write minimal TOML directly (as a user would) so unrelated keys are not overwritten.
	os.WriteFile(projectPath, []byte("[storage]\nmode = \"local\"\n\n[storage.local]\ndriver = \"json\"\n"), 0644)

	envCfg := Defaults()
	envCfg.Storage.Mode = "local"
	envCfg.Storage.Local.Driver = "postgres"
	Save(envCfg, envPath)

	t.Run("env_var_highest_precedence", func(t *testing.T) {
		t.Setenv("BENCH_CONFIG", envPath)

		cfg, src, err := LoadWithGlobalPath(globalPath)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if src.Origin != "env" {
			t.Fatalf("expected origin=env, got %s", src.Origin)
		}
		if cfg.Storage.Local.Driver != "postgres" {
			t.Fatalf("expected driver=postgres from env, got %s", cfg.Storage.Local.Driver)
		}
	})

	t.Run("project_overrides_global", func(t *testing.T) {
		t.Setenv("BENCH_CONFIG", "")

		origDir, _ := os.Getwd()
		if err := os.Chdir(projectDir); err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(origDir)

		cfg, src, err := LoadWithGlobalPath(globalPath)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if src.Origin != "project" {
			t.Fatalf("expected origin=project, got %s", src.Origin)
		}
		// storage.mode should come from project
		if cfg.Storage.Mode != "local" {
			t.Fatalf("expected mode=local from project, got %s", cfg.Storage.Mode)
		}
		// cloud.api_url should be inherited from global
		if cfg.Cloud.APIURL != "https://global.example.com" {
			t.Fatalf("expected api_url from global, got %s", cfg.Cloud.APIURL)
		}
	})

	t.Run("global_fallback", func(t *testing.T) {
		t.Setenv("BENCH_CONFIG", "")

		tmpDir := t.TempDir()
		origDir, _ := os.Getwd()
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(origDir)

		cfg, src, err := LoadWithGlobalPath(globalPath)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if src.Origin != "global" {
			t.Fatalf("expected origin=global, got %s", src.Origin)
		}
		if cfg.Storage.Mode != "cloud" {
			t.Fatalf("expected mode=cloud from global, got %s", cfg.Storage.Mode)
		}
	})

	t.Run("no_config_returns_error", func(t *testing.T) {
		t.Setenv("BENCH_CONFIG", "")

		tmpDir := t.TempDir()
		origDir, _ := os.Getwd()
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(origDir)

		_, _, err := LoadWithGlobalPath(filepath.Join(tmpDir, "nonexistent.toml"))
		if err == nil {
			t.Fatal("expected error when no config exists")
		}
	})
}

func TestGetSet(t *testing.T) {
	cfg := Defaults()

	// Get returns correct default
	v, err := cfg.Get("storage.mode")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v != "local" {
		t.Fatalf("expected 'local', got %q", v)
	}

	// Set updates the value
	if err := cfg.Set("storage.mode", "cloud"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, _ = cfg.Get("storage.mode")
	if v != "cloud" {
		t.Fatalf("expected 'cloud' after Set, got %q", v)
	}

	// Unknown key errors
	if _, err := cfg.Get("nonexistent"); err == nil {
		t.Fatal("expected error for unknown Get key")
	}
	if err := cfg.Set("nonexistent", "x"); err == nil {
		t.Fatal("expected error for unknown Set key")
	}

	// Validation errors
	if err := cfg.Set("storage.mode", "invalid"); err == nil {
		t.Fatal("expected error for invalid mode value")
	}
	if err := cfg.Set("storage.local.driver", "sqlite"); err == nil {
		t.Fatal("expected error for invalid driver value")
	}
	if err := cfg.Set("storage.local.postgres.port", "not-a-number"); err == nil {
		t.Fatal("expected error for non-numeric port")
	}
}

func TestGetSetPortRoundTrip(t *testing.T) {
	cfg := Defaults()

	if err := cfg.Set("storage.local.postgres.port", "9999"); err != nil {
		t.Fatalf("Set port: %v", err)
	}
	v, _ := cfg.Get("storage.local.postgres.port")
	if v != "9999" {
		t.Fatalf("expected port 9999, got %s", v)
	}
}

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		input string
		want  string
	}{
		{"~/foo/bar", filepath.Join(home, "foo", "bar")},
		{"~", home},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
	}

	for _, tt := range tests {
		got := ExpandPath(tt.input)
		if got != tt.want {
			t.Errorf("ExpandPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsStorageConfigured(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{
			name: "local with driver",
			cfg:  Config{Storage: StorageConfig{Mode: "local", Local: LocalStorageConfig{Driver: "json"}}},
			want: true,
		},
		{
			name: "cloud with api_url",
			cfg:  Config{Storage: StorageConfig{Mode: "cloud"}, Cloud: CloudConfig{APIURL: "https://example.com"}},
			want: true,
		},
		{
			name: "empty mode",
			cfg:  Config{},
			want: false,
		},
		{
			name: "cloud without api_url",
			cfg:  Config{Storage: StorageConfig{Mode: "cloud"}},
			want: false,
		},
		{
			name: "local without driver",
			cfg:  Config{Storage: StorageConfig{Mode: "local"}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.IsStorageConfigured()
			if got != tt.want {
				t.Errorf("IsStorageConfigured() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSaveCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c", "config.toml")

	cfg := Defaults()
	if err := Save(cfg, deep); err != nil {
		t.Fatalf("Save to nested path: %v", err)
	}

	if _, err := os.Stat(deep); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestLoadFromPathInvalid(t *testing.T) {
	// Nonexistent file
	if _, err := LoadFromPath("/nonexistent/config.toml"); err == nil {
		t.Fatal("expected error for nonexistent file")
	}

	// Invalid TOML
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.toml")
	os.WriteFile(bad, []byte("not valid toml [[["), 0644)
	if _, err := LoadFromPath(bad); err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}

// --- helpers ---

func assertStr(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %q, want %q", field, got, want)
	}
}

func assertInt(t *testing.T, field string, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %d, want %d", field, got, want)
	}
}

func assertBool(t *testing.T, field string, got, want bool) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", field, got, want)
	}
}
