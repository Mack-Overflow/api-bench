package db

import (
	"database/sql"
	"time"
)

type DB struct {
	*sql.DB
}

func New(db *sql.DB) *DB {
	return &DB{DB: db}
}

func WithTx[T any](db *DB, fn func(tx *sql.Tx) (T, error)) (T, error) {
	var zero T

	tx, err := db.Begin()
	if err != nil {
		return zero, err
	}

	defer tx.Rollback()

	val, err := fn(tx)
	if err != nil {
		return zero, err
	}

	if err := tx.Commit(); err != nil {
		return zero, err
	}

	return val, nil
}

func DefaultPool(db *sql.DB) {
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)
}
