package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/Mack-Overflow/api-bench/config"
	"github.com/Mack-Overflow/api-bench/storage"
)

func configCmd(args []string) error {
	if len(args) == 0 {
		printConfigUsage()
		return nil
	}

	switch args[0] {
	case "init":
		return configInitCmd()
	case "set":
		return configSetCmd(args[1:])
	case "get":
		return configGetCmd(args[1:])
	case "show":
		return configShowCmd()
	case "test":
		return configTestCmd()
	case "help", "--help", "-h":
		printConfigUsage()
		return nil
	default:
		printConfigUsage()
		return fmt.Errorf("unknown config command: %s", args[0])
	}
}

func printConfigUsage() {
	fmt.Print(`benchmarkr config - Manage benchmarkr configuration

Usage:
  benchmarkr config <command>

Commands:
  init    Interactive setup wizard
  set     Set a configuration value (benchmarkr config set <key> <value>)
  get     Get a configuration value (benchmarkr config get <key>)
  show    Show resolved configuration with source annotations
  test    Verify the active storage backend is reachable
`)
}

// --- init ---

func configInitCmd() error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println()
	fmt.Println("  Benchmarkr Configuration Setup")
	fmt.Println("  ==============================")
	fmt.Println()

	// 1. Storage mode
	fmt.Print("  Storage mode (local/cloud) [local]: ")
	mode := readLine(reader)
	if mode == "" {
		mode = "local"
	}
	if mode != "local" && mode != "cloud" {
		return fmt.Errorf("invalid storage mode: %s (must be 'local' or 'cloud')", mode)
	}

	cfg := config.Defaults()
	cfg.Storage.Mode = mode

	if mode == "local" {
		// 2. Driver selection
		fmt.Print("  Storage driver (json/postgres/mysql) [json]: ")
		driver := readLine(reader)
		if driver == "" {
			driver = "json"
		}
		if driver != "json" && driver != "postgres" && driver != "mysql" {
			return fmt.Errorf("invalid driver: %s (must be 'json', 'postgres', or 'mysql')", driver)
		}
		cfg.Storage.Local.Driver = driver

		// 3. Driver-specific prompts
		switch driver {
		case "json":
			defaultDir := cfg.Storage.Local.JSON.OutputDir
			fmt.Printf("  Output directory [%s]: ", defaultDir)
			if dir := readLine(reader); dir != "" {
				cfg.Storage.Local.JSON.OutputDir = dir
			}

		case "postgres":
			promptPostgres(reader, &cfg.Storage.Local.Postgres)

		case "mysql":
			promptMySQL(reader, &cfg.Storage.Local.MySQL)
		}
	} else {
		// Cloud mode prompts
		fmt.Printf("  Cloud API URL [%s]: ", cfg.Cloud.APIURL)
		if v := readLine(reader); v != "" {
			cfg.Cloud.APIURL = v
		}

		fmt.Printf("  Token env var name [%s]: ", cfg.Cloud.TokenEnv)
		if v := readLine(reader); v != "" {
			cfg.Cloud.TokenEnv = v
		}
	}

	path := config.DefaultGlobalPath()
	if err := config.Save(cfg, path); err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("  Config written to %s\n", path)
	fmt.Println()
	return nil
}

func promptPostgres(reader *bufio.Reader, pg *config.PostgresDriverConfig) {
	fmt.Printf("  PostgreSQL host [%s]: ", pg.Host)
	if v := readLine(reader); v != "" {
		pg.Host = v
	}

	fmt.Printf("  PostgreSQL port [%d]: ", pg.Port)
	if v := readLine(reader); v != "" {
		fmt.Sscanf(v, "%d", &pg.Port)
	}

	fmt.Printf("  Database name [%s]: ", pg.Database)
	if v := readLine(reader); v != "" {
		pg.Database = v
	}

	fmt.Printf("  Username [%s]: ", pg.User)
	if v := readLine(reader); v != "" {
		pg.User = v
	}

	fmt.Printf("  Password env var [%s]: ", pg.PasswordEnv)
	if v := readLine(reader); v != "" {
		pg.PasswordEnv = v
	}

	fmt.Print("  SSL enabled (true/false) [false]: ")
	if v := readLine(reader); v == "true" {
		pg.SSL = true
	}
}

func promptMySQL(reader *bufio.Reader, my *config.MySQLDriverConfig) {
	fmt.Printf("  MySQL host [%s]: ", my.Host)
	if v := readLine(reader); v != "" {
		my.Host = v
	}

	fmt.Printf("  MySQL port [%d]: ", my.Port)
	if v := readLine(reader); v != "" {
		fmt.Sscanf(v, "%d", &my.Port)
	}

	fmt.Printf("  Database name [%s]: ", my.Database)
	if v := readLine(reader); v != "" {
		my.Database = v
	}

	fmt.Printf("  Username [%s]: ", my.User)
	if v := readLine(reader); v != "" {
		my.User = v
	}

	fmt.Printf("  Password env var [%s]: ", my.PasswordEnv)
	if v := readLine(reader); v != "" {
		my.PasswordEnv = v
	}
}

func readLine(reader *bufio.Reader) string {
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}

// --- set ---

func configSetCmd(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: benchmarkr config set <key> <value>")
	}

	key, value := args[0], args[1]

	cfg, source, err := config.Load()
	if err != nil {
		// No config exists yet — start from defaults
		cfg = config.Defaults()
		// Respect BENCH_CONFIG env var for the save path; otherwise use global default
		if p := os.Getenv("BENCH_CONFIG"); p != "" {
			source = &config.Source{Path: p, Origin: "env"}
		} else {
			source = &config.Source{Path: config.DefaultGlobalPath(), Origin: "global"}
		}
	}

	if err := cfg.Set(key, value); err != nil {
		return err
	}

	if err := config.Save(cfg, source.Path); err != nil {
		return err
	}

	fmt.Printf("  %s = %s (saved to %s)\n", key, value, source.Path)
	return nil
}

// --- get ---

func configGetCmd(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: benchmarkr config get <key>")
	}

	cfg, _, err := config.Load()
	if err != nil {
		return err
	}

	value, err := cfg.Get(args[0])
	if err != nil {
		return err
	}

	fmt.Println(value)
	return nil
}

// --- show ---

func configShowCmd() error {
	cfg, source, err := config.Load()
	if err != nil {
		return err
	}

	fmt.Printf("# Resolved from: %s (%s)\n\n", source.Path, source.Origin)
	return toml.NewEncoder(os.Stdout).Encode(cfg)
}

// --- test ---

func configTestCmd() error {
	cfg, source, err := config.Load()
	if err != nil {
		return err
	}

	fmt.Printf("  Config: %s (%s)\n", source.Path, source.Origin)
	fmt.Printf("  Mode:   %s\n", cfg.Storage.Mode)
	fmt.Println()

	switch cfg.Storage.Mode {
	case "local":
		return testLocalStorage(cfg)
	case "cloud":
		return testCloudStorage(cfg)
	default:
		return fmt.Errorf("unknown storage mode: %s", cfg.Storage.Mode)
	}
}

func testLocalStorage(cfg *config.Config) error {
	switch cfg.Storage.Local.Driver {
	case "json":
		dir := config.ExpandPath(cfg.Storage.Local.JSON.OutputDir)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("cannot create output directory %s: %w", dir, err)
		}
		testFile := filepath.Join(dir, ".benchmarkr_test_write")
		if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
			return fmt.Errorf("cannot write to %s: %w", dir, err)
		}
		os.Remove(testFile)
		fmt.Printf("  JSON storage OK — writing to %s\n", dir)
		return nil

	case "postgres":
		pg := cfg.Storage.Local.Postgres
		fmt.Printf("  PostgreSQL: %s@%s:%d/%s\n", pg.User, pg.Host, pg.Port, pg.Database)
		if err := storage.TestConnection(cfg); err != nil {
			return fmt.Errorf("PostgreSQL connection failed: %w", err)
		}
		fmt.Println("  PostgreSQL storage OK — connection verified")
		return nil

	case "mysql":
		my := cfg.Storage.Local.MySQL
		fmt.Printf("  MySQL: %s@%s:%d/%s\n", my.User, my.Host, my.Port, my.Database)
		if err := storage.TestConnection(cfg); err != nil {
			return fmt.Errorf("MySQL connection failed: %w", err)
		}
		fmt.Println("  MySQL storage OK — connection verified")
		return nil

	default:
		return fmt.Errorf("unknown driver: %s", cfg.Storage.Local.Driver)
	}
}

func testCloudStorage(cfg *config.Config) error {
	tokenEnv := cfg.Cloud.TokenEnv
	if tokenEnv != "" {
		if os.Getenv(tokenEnv) != "" {
			fmt.Printf("  Token:   set via $%s\n", tokenEnv)
		} else {
			fmt.Printf("  Warning: $%s is not set\n", tokenEnv)
		}
	}
	fmt.Printf("  API URL: %s\n", cfg.Cloud.APIURL)

	dbURL := os.Getenv("DB_URL")
	if dbURL != "" {
		fmt.Println("  DB_URL:  set (direct database access available)")
	} else {
		fmt.Println("  DB_URL:  not set (direct database access unavailable)")
	}
	return nil
}
