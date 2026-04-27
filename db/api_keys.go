package db

import (
	"crypto/sha256"
	"encoding/hex"
)

// GetUserIDByAPIKey hashes the given API key and looks up the corresponding
// user_id in the api_keys table. Returns sql.ErrNoRows if the key is invalid.
func (db *DB) GetUserIDByAPIKey(apiKey string) (int64, error) {
	hash := sha256.Sum256([]byte(apiKey))
	keyHash := hex.EncodeToString(hash[:])

	var userID int64
	err := db.QueryRow(
		`SELECT user_id FROM api_keys WHERE key_hash = $1`,
		keyHash,
	).Scan(&userID)
	return userID, err
}
