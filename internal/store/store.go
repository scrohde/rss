// Package store provides SQLite-backed persistence helpers for feeds and items.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	_ "modernc.org/sqlite" // Register the sqlite database/sql driver.
)

// Open is part of the store package API.
func Open(path string) (*sql.DB, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	// SQLite behaves best with a single connection for this workload.
	db.SetMaxOpenConns(1)

	_, err = db.ExecContext(context.Background(), "PRAGMA journal_mode=WAL;")
	if err != nil {
		return nil, fmt.Errorf("enable WAL mode: %w", err)
	}

	return db, nil
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}

	return ctx
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}

	return value
}

func nullTimeToValue(value sql.NullTime) any {
	if value.Valid {
		return value.Time
	}

	return nil
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	return value
}

func rollbackTx(tx *sql.Tx) {
	err := tx.Rollback()
	if err != nil && !errors.Is(err, sql.ErrTxDone) {
		slog.Warn("tx rollback failed", "err", err)
	}
}
