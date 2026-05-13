package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/Mack-Overflow/api-bench/config"
	"github.com/Mack-Overflow/api-bench/db"
	mcpserver "github.com/Mack-Overflow/api-bench/mcp"
	"github.com/Mack-Overflow/api-bench/storage"
	"github.com/mark3labs/mcp-go/server"

	"github.com/joho/godotenv"
)

func main() {
	envFile := flag.Bool("env-file", false, "Load .env file from working directory (off by default for security)")
	flag.Parse()

	if *envFile {
		_ = godotenv.Load()
	}

	var (
		backend storage.StorageBackend
		cfg     *config.Config
	)
	if loaded, _, err := config.Load(); err == nil && loaded.IsStorageConfigured() {
		cfg = loaded
		if b, err := storage.NewBackendFromConfig(cfg); err == nil {
			backend = b
		} else {
			log.Printf("warning: storage backend init failed: %v", err)
		}
	}

	var store *db.DB
	if dsn := os.Getenv("DB_URL"); dsn != "" {
		sqlDB, err := db.OpenDB(dsn)
		if err != nil {
			log.Printf("warning: could not connect to database: %v", err)
		} else {
			store = db.New(sqlDB)
			defer sqlDB.Close()
		}
	}

	s := mcpserver.NewServer(backend, cfg, store)

	stdioServer := server.NewStdioServer(s)
	if err := stdioServer.Listen(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatalf("mcp server error: %v", err)
	}
}
