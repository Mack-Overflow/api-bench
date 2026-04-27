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

func UpdateEndpointTx(
	tx *sql.Tx,
	endpointID int64,
	method string,
	url string,
	headers []byte,
	params []byte,
	body []byte,
) error {
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

	_, err := tx.Exec(`
		UPDATE endpoints
		SET method = $1,
			url = $2,
			headers = $3::json,
			params = $4::json,
			body = $5::json
		WHERE id = $6
	`,
		method,
		url,
		headersValue,
		paramsValue,
		bodyValue,
		endpointID,
	)
	return err
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

// EndpointVersion is one revision of an endpoint's request configuration.
type EndpointVersion struct {
	ID         int64           `json:"id"`
	EndpointID int64           `json:"endpoint_id"`
	Version    int             `json:"version"`
	Method     string          `json:"method"`
	URL        string          `json:"url"`
	Headers    json.RawMessage `json:"headers,omitempty"`
	Params     json.RawMessage `json:"params,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// GetEndpointByName returns the most recent endpoint with the given name for
// the user. Pass nil for anonymous (user_id IS NULL). Returns sql.ErrNoRows
// when not found.
func (db *DB) GetEndpointByName(userID *int64, name string) (*Endpoint, error) {
	var (
		row *sql.Row
	)
	if userID == nil {
		row = db.QueryRow(`
			SELECT id, name, method, url, headers, params, user_id, created_at
			FROM endpoints
			WHERE user_id IS NULL AND name = $1
			ORDER BY id DESC
			LIMIT 1
		`, name)
	} else {
		row = db.QueryRow(`
			SELECT id, name, method, url, headers, params, user_id, created_at
			FROM endpoints
			WHERE user_id = $1 AND name = $2
			ORDER BY id DESC
			LIMIT 1
		`, *userID, name)
	}

	var e Endpoint
	if err := row.Scan(&e.ID, &e.Name, &e.Method, &e.URL, &e.Headers, &e.Params, &e.UserID, &e.CreatedAt); err != nil {
		return nil, err
	}
	return &e, nil
}

// GetEndpointVersion returns a specific version of an endpoint. Returns
// sql.ErrNoRows when not found.
func (db *DB) GetEndpointVersion(endpointID int64, version int) (*EndpointVersion, error) {
	row := db.QueryRow(`
		SELECT id, endpoint_id, version, method, url, headers, params, body, created_at
		FROM endpoint_versions
		WHERE endpoint_id = $1 AND version = $2
		LIMIT 1
	`, endpointID, version)

	var v EndpointVersion
	if err := row.Scan(&v.ID, &v.EndpointID, &v.Version, &v.Method, &v.URL, &v.Headers, &v.Params, &v.Body, &v.CreatedAt); err != nil {
		return nil, err
	}
	return &v, nil
}

// GetLatestEndpointVersion returns the highest-numbered version of an endpoint.
// Returns sql.ErrNoRows when the endpoint has no versions yet.
func (db *DB) GetLatestEndpointVersion(endpointID int64) (*EndpointVersion, error) {
	row := db.QueryRow(`
		SELECT id, endpoint_id, version, method, url, headers, params, body, created_at
		FROM endpoint_versions
		WHERE endpoint_id = $1
		ORDER BY version DESC
		LIMIT 1
	`, endpointID)

	var v EndpointVersion
	if err := row.Scan(&v.ID, &v.EndpointID, &v.Version, &v.Method, &v.URL, &v.Headers, &v.Params, &v.Body, &v.CreatedAt); err != nil {
		return nil, err
	}
	return &v, nil
}

// GetMaxEndpointVersionTx returns the highest version number for an endpoint,
// or 0 if no versions exist.
func GetMaxEndpointVersionTx(tx *sql.Tx, endpointID int64) (int, error) {
	var maxVersion sql.NullInt64
	err := tx.QueryRow(`
		SELECT MAX(version) FROM endpoint_versions WHERE endpoint_id = $1
	`, endpointID).Scan(&maxVersion)
	if err != nil {
		return 0, err
	}
	if !maxVersion.Valid {
		return 0, nil
	}
	return int(maxVersion.Int64), nil
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
