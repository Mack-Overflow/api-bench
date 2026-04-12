package db

import "database/sql"

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
