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

func (db *DB) WithTx(fn func(tx *sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}

	defer tx.Rollback()

	if err := fn(tx); err != nil {
		return err
	}

	return tx.Commit()
}

func DefaultPool(db *sql.DB) {
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)
}
