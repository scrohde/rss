package store

import (
	"context"
	"database/sql"
	"fmt"
)

const authSchemaSQL = `
CREATE TABLE IF NOT EXISTS auth_users (
	id INTEGER PRIMARY KEY,
	user_handle BLOB NOT NULL UNIQUE,
	name TEXT NOT NULL,
	display_name TEXT NOT NULL,
	appearance_theme TEXT NOT NULL DEFAULT 'system',
	created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS auth_webauthn_credentials (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	credential_id BLOB NOT NULL UNIQUE,
	public_key BLOB NOT NULL,
	sign_count INTEGER NOT NULL,
	aaguid BLOB NOT NULL,
	backup_eligible INTEGER,
	backup_state INTEGER,
	transports TEXT NOT NULL,
	created_at DATETIME NOT NULL,
	last_used_at DATETIME,
	FOREIGN KEY(user_id) REFERENCES auth_users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS auth_sessions (
	session_id TEXT PRIMARY KEY,
	session_token_hash BLOB NOT NULL,
	csrf_token TEXT NOT NULL,
	user_id INTEGER NOT NULL,
	created_at DATETIME NOT NULL,
	expires_at DATETIME NOT NULL,
	last_seen_at DATETIME NOT NULL,
	revoked_at DATETIME,
	FOREIGN KEY(user_id) REFERENCES auth_users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS auth_webauthn_challenges (
	challenge_id TEXT PRIMARY KEY,
	flow TEXT NOT NULL,
	client_key TEXT NOT NULL DEFAULT '',
	challenge_blob BLOB NOT NULL,
	expires_at DATETIME NOT NULL,
	used_at DATETIME,
	user_id INTEGER,
	created_at DATETIME NOT NULL,
	FOREIGN KEY(user_id) REFERENCES auth_users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS auth_recovery_codes (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	code_hash BLOB NOT NULL UNIQUE,
	created_at DATETIME NOT NULL,
	used_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_auth_challenges_expiry
ON auth_webauthn_challenges (expires_at);

CREATE INDEX IF NOT EXISTS idx_auth_sessions_expiry
ON auth_sessions (expires_at);
`

func ensureAuthSchema(db *sql.DB) error {
	_, err := db.ExecContext(context.Background(), authSchemaSQL)
	if err != nil {
		return fmt.Errorf("initialize auth schema: %w", err)
	}

	err = ensureAuthUserAppearanceThemeColumn(db)
	if err != nil {
		return err
	}

	err = ensureAuthCredentialFlagColumn(db, "backup_eligible")
	if err != nil {
		return err
	}

	err = ensureAuthCredentialFlagColumn(db, "backup_state")
	if err != nil {
		return err
	}

	err = ensureAuthChallengeClientKeyColumn(db)
	if err != nil {
		return err
	}

	return nil
}

func ensureAuthChallengeClientKeyColumn(db *sql.DB) error {
	hasColumn, err := authTableHasColumn(db, "auth_webauthn_challenges", "client_key")
	if err != nil {
		return fmt.Errorf("check auth challenge client key column: %w", err)
	}

	if hasColumn {
		return nil
	}

	_, err = db.ExecContext(context.Background(),
		`ALTER TABLE auth_webauthn_challenges ADD COLUMN client_key TEXT NOT NULL DEFAULT ''`)
	if err != nil {
		return fmt.Errorf("add auth challenge client key column: %w", err)
	}

	return nil
}

func ensureAuthUserAppearanceThemeColumn(db *sql.DB) error {
	hasColumn, err := authTableHasColumn(db, "auth_users", "appearance_theme")
	if err != nil {
		return fmt.Errorf("check auth user appearance theme column: %w", err)
	}

	if !hasColumn {
		_, err = db.ExecContext(
			context.Background(),
			`ALTER TABLE auth_users ADD COLUMN appearance_theme TEXT NOT NULL DEFAULT 'system'`,
		)
		if err != nil {
			return fmt.Errorf("add auth user appearance theme column: %w", err)
		}
	}

	_, err = db.ExecContext(
		context.Background(),
		`UPDATE auth_users SET appearance_theme = ? WHERE appearance_theme IS NULL OR appearance_theme = ''`,
		AuthAppearanceThemeSystem,
	)
	if err != nil {
		return fmt.Errorf("backfill auth user appearance theme column: %w", err)
	}

	return nil
}

func authTableHasColumn(db *sql.DB, tableName, columnName string) (bool, error) {
	var count int

	err := db.QueryRowContext(
		context.Background(),
		fmt.Sprintf(`SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name = ?`, tableName),
		columnName,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("lookup column %q on %q: %w", columnName, tableName, err)
	}

	return count > 0, nil
}

func ensureAuthCredentialFlagColumn(db *sql.DB, column string) error {
	hasColumn, err := authTableHasColumn(db, "auth_webauthn_credentials", column)
	if err != nil {
		return fmt.Errorf("check auth credential column %q: %w", column, err)
	}

	if hasColumn {
		return nil
	}

	statement, err := authCredentialAlterColumnStatement(column)
	if err != nil {
		return err
	}

	_, err = db.ExecContext(context.Background(), statement)
	if err != nil {
		return fmt.Errorf("add auth credential column %q: %w", column, err)
	}

	return nil
}

func authCredentialAlterColumnStatement(column string) (string, error) {
	switch column {
	case "backup_eligible":
		return "ALTER TABLE auth_webauthn_credentials ADD COLUMN backup_eligible INTEGER", nil
	case "backup_state":
		return "ALTER TABLE auth_webauthn_credentials ADD COLUMN backup_state INTEGER", nil
	default:
		return "", fmt.Errorf("%w %q", errUnsupportedAuthCredentialColumn, column)
	}
}
