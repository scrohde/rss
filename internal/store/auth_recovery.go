package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ReplaceRecoveryCodeHash stores one single-use recovery code hash.
func ReplaceRecoveryCodeHash(ctx context.Context, db *sql.DB, codeHash []byte) error {
	ctx = contextOrBackground(ctx)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace recovery code transaction: %w", err)
	}

	_, err = tx.ExecContext(ctx, `DELETE FROM auth_recovery_codes WHERE used_at IS NULL`)
	if err != nil {
		rollbackTx(tx)

		return fmt.Errorf("delete existing recovery codes: %w", err)
	}

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO auth_recovery_codes (code_hash, created_at, used_at) VALUES (?, ?, NULL)`,
		codeHash,
		time.Now().UTC(),
	)
	if err != nil {
		rollbackTx(tx)

		return fmt.Errorf("insert recovery code: %w", err)
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("commit replace recovery code transaction: %w", err)
	}

	return nil
}

// ConsumeRecoveryCodeHash marks a recovery code as used.
func ConsumeRecoveryCodeHash(ctx context.Context, db *sql.DB, codeHash []byte) (bool, error) {
	ctx = contextOrBackground(ctx)

	result, err := db.ExecContext(
		ctx,
		`UPDATE auth_recovery_codes SET used_at = ? WHERE code_hash = ? AND used_at IS NULL`,
		time.Now().UTC(),
		codeHash,
	)
	if err != nil {
		return false, fmt.Errorf("consume recovery code: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count consumed recovery code rows: %w", err)
	}

	return affected == 1, nil
}

// HasUnusedRecoveryCode returns true when a recovery code is currently active.
func HasUnusedRecoveryCode(ctx context.Context, db *sql.DB) (bool, error) {
	ctx = contextOrBackground(ctx)

	var count int

	err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM auth_recovery_codes WHERE used_at IS NULL`,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("count unused recovery codes: %w", err)
	}

	return count > 0, nil
}
