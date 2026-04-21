package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/Mack-Overflow/api-bench/db"

	"github.com/joho/godotenv"
)

func generateRunID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	dsn := os.Getenv("DB_URL")
	sqlDB, err := db.OpenDB(dsn)
	if err != nil {
		log.Fatal(err)
	}

	store := db.New(sqlDB)

	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// Protected routes — require valid API key
	protectedMux := http.NewServeMux()
	protectedMux.HandleFunc("/benchmarks/start", startBenchmarkHandler(store))
	protectedMux.HandleFunc("/benchmarks/stop", stopBenchmarkHandler)
	mux.Handle("/benchmarks/start", withAPIKeyAuth(store, protectedMux))
	mux.Handle("/benchmarks/stop", withAPIKeyAuth(store, protectedMux))

	// Public routes — run_id acts as capability token
	mux.HandleFunc("/benchmarks/status", getBenchmarkStatusHandler)
	mux.HandleFunc("/benchmarks/stream", benchmarkStreamHandler)

	handler := withCORS(mux)

	log.Fatal(http.ListenAndServe(":"+port, handler))
}
