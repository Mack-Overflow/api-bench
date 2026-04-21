package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/Mack-Overflow/api-bench/db"
)

type contextKey string

const userIDKey contextKey = "user_id"

func withAPIKeyAuth(store *db.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "missing or invalid authorization header", http.StatusUnauthorized)
			return
		}

		apiKey := strings.TrimPrefix(auth, "Bearer ")
		if apiKey == "" {
			http.Error(w, "empty api key", http.StatusUnauthorized)
			return
		}

		hash := sha256.Sum256([]byte(apiKey))
		keyHash := hex.EncodeToString(hash[:])

		var userID int64
		err := store.QueryRow(
			"SELECT user_id FROM api_keys WHERE key_hash = $1",
			keyHash,
		).Scan(&userID)

		if err != nil {
			http.Error(w, "invalid api key", http.StatusUnauthorized)
			return
		}

		// Update last_used_at in the background
		go func() {
			store.Exec(
				"UPDATE api_keys SET last_used_at = $1 WHERE key_hash = $2",
				time.Now(), keyHash,
			)
		}()

		ctx := context.WithValue(r.Context(), userIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
