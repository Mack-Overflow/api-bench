package db

import (
	"database/sql"
	"encoding/json"
	"time"
)

func InsertEndpointVersionTx(
	tx *sql.Tx,
	endpointID int64,
	endpointVersion int64,
	method string,
	headers []byte,
	params []byte,
	body []byte,
	url string,
) (int64, error) {
	var versionID int64

	var headersValue interface{} = headers
	var paramsValue interface{} = params
	var bodyValue interface{} = body

	if len(headers) == 0 {
		headersValue = ""
	}
	if len(params) == 0 {
		paramsValue = nil
	}
	if len(body) == 0 {
		bodyValue = nil
	}
	err := tx.QueryRow(`
		INSERT INTO endpoint_versions (
			endpoint_id,
			version,
			method,
			url,
			params,
			body,
			headers,
			created_at
		) VALUES ($1, $2, $3, $4, $5::json, $6::json, $7::json, NOW())
		RETURNING id
	`,
		endpointID,
		endpointVersion,
		method,
		url,
		paramsValue,
		bodyValue,
		headersValue,
	).Scan(&versionID)

	return versionID, err
}

func InsertEndpointTx(
	tx *sql.Tx,
	name string,
	method string,
	url string,
	headers []byte,
	params []byte,
	body []byte,
	userID *int64,
) (int64, error) {
	var endpointID int64

	var headersValue interface{} = headers
	var paramsValue interface{} = params
	var bodyValue interface{} = body

	if len(headers) == 0 {
		headersValue = nil
	}
	if len(params) == 0 {
		paramsValue = nil
	}
	if len(body) == 0 {
		bodyValue = nil
	}
	err := tx.QueryRow(`
		INSERT INTO endpoints (
			name,
			method,
			url,
			headers,
			params,
			body,
			user_id,
			created_at
		) VALUES ($1, $2, $3, $4::json, $5::json, $6::json, $7, NOW())
		RETURNING id
	`,
		name,
		method,
		url,
		headersValue,
		paramsValue,
		bodyValue,
		userID,
	).Scan(&endpointID)
	return endpointID, err
}

// Endpoint represents a saved API endpoint.
type Endpoint struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Method    string          `json:"method"`
	URL       string          `json:"url"`
	Headers   json.RawMessage `json:"headers,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	UserID    *int64          `json:"user_id,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// ListEndpoints returns saved endpoints ordered by most recent first.
func (db *DB) ListEndpoints(limit, offset int) ([]Endpoint, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := db.Query(`
		SELECT id, name, method, url, headers, params, user_id, created_at
		FROM endpoints
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var endpoints []Endpoint
	for rows.Next() {
		var e Endpoint
		if err := rows.Scan(&e.ID, &e.Name, &e.Method, &e.URL, &e.Headers, &e.Params, &e.UserID, &e.CreatedAt); err != nil {
			return nil, err
		}
		endpoints = append(endpoints, e)
	}
	return endpoints, rows.Err()
}
