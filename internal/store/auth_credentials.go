package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"
)

// AuthCredentialCount returns the number of registered credentials.
func AuthCredentialCount(ctx context.Context, db *sql.DB) (int, error) {
	ctx = contextOrBackground(ctx)

	var count int

	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM auth_webauthn_credentials").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count auth credentials: %w", err)
	}

	return count, nil
}

// ListAuthCredentialsByUser lists all credentials for a given auth user.
func ListAuthCredentialsByUser(ctx context.Context, db *sql.DB, userID int64) ([]AuthCredentialRecord, error) {
	ctx = contextOrBackground(ctx)

	rows, err := queryAuthCredentialsByUser(ctx, db, userID)
	if err != nil {
		return nil, err
	}

	defer func() {
		closeRows(rows)
	}()

	credentials := make([]AuthCredentialRecord, 0)

	for rows.Next() {
		credential, scanErr := scanAuthCredentialRow(rows)
		if scanErr != nil {
			return nil, scanErr
		}

		credentials = append(credentials, *credential)
	}

	rowsErr := rows.Err()
	if rowsErr != nil {
		return nil, fmt.Errorf("iterate auth credential rows: %w", rowsErr)
	}

	return credentials, nil
}

func queryAuthCredentialsByUser(ctx context.Context, db *sql.DB, userID int64) (*sql.Rows, error) {
	rows, err := db.QueryContext(ctx, `
	SELECT
		id, user_id, credential_id, public_key, sign_count, aaguid,
		backup_eligible, backup_state, transports, created_at, last_used_at
	FROM auth_webauthn_credentials
	WHERE user_id = ?
	ORDER BY id ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list auth credentials for user %d: %w", userID, err)
	}

	return rows, nil
}

func scanAuthCredentialRow(scanner interface {
	Scan(dest ...any) error
},
) (*AuthCredentialRecord, error) {
	record := new(AuthCredentialRecord)

	var signCount int64

	err := scanner.Scan(
		&record.ID,
		&record.UserID,
		&record.CredentialID,
		&record.PublicKey,
		&signCount,
		&record.AAGUID,
		&record.BackupEligible,
		&record.BackupState,
		&record.Transports,
		&record.CreatedAt,
		&record.LastUsedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan auth credential row: %w", err)
	}

	record.SignCount, err = safeSignCountUint32(signCount)
	if err != nil {
		return nil, err
	}

	return record, nil
}

func closeRows(rows *sql.Rows) {
	err := rows.Close()
	if err != nil {
		return
	}
}

func safeSignCountUint32(value int64) (uint32, error) {
	if value < 0 || value > math.MaxUint32 {
		return 0, fmt.Errorf("%w: %d", errInvalidAuthCredentialSignCount, value)
	}

	return uint32(value), nil
}

// GetAuthCredentialByID loads a credential by raw credential ID.
func GetAuthCredentialByID(ctx context.Context, db *sql.DB, credentialID []byte) (AuthCredentialRecord, error) {
	ctx = contextOrBackground(ctx)

	var (
		credential AuthCredentialRecord
		signCount  int64
	)

	err := db.QueryRowContext(ctx, `
	SELECT
	id,
	user_id,
	credential_id,
	public_key,
	sign_count,
	aaguid,
	backup_eligible,
	backup_state,
	transports,
	created_at,
	last_used_at
FROM auth_webauthn_credentials
WHERE credential_id = ?
	`, credentialID).Scan(
		&credential.ID,
		&credential.UserID,
		&credential.CredentialID,
		&credential.PublicKey,
		&signCount,
		&credential.AAGUID,
		&credential.BackupEligible,
		&credential.BackupState,
		&credential.Transports,
		&credential.CreatedAt,
		&credential.LastUsedAt,
	)
	if err != nil {
		return AuthCredentialRecord{}, fmt.Errorf("load auth credential: %w", err)
	}

	credential.SignCount, err = safeSignCountUint32(signCount)
	if err != nil {
		return AuthCredentialRecord{}, err
	}

	return credential, nil
}

// UpsertAuthCredential inserts or updates a WebAuthn credential.
func UpsertAuthCredential(ctx context.Context, db *sql.DB, credential *AuthCredentialRecord) error {
	ctx = contextOrBackground(ctx)

	_, err := db.ExecContext(ctx, `
	INSERT INTO auth_webauthn_credentials
	(
		user_id, credential_id, public_key, sign_count, aaguid,
		backup_eligible, backup_state, transports, created_at, last_used_at
	)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(credential_id)
DO UPDATE SET
	user_id = excluded.user_id,
	public_key = excluded.public_key,
	sign_count = excluded.sign_count,
	aaguid = excluded.aaguid,
	backup_eligible = excluded.backup_eligible,
	backup_state = excluded.backup_state,
	transports = excluded.transports,
	last_used_at = excluded.last_used_at
	`,
		credential.UserID,
		credential.CredentialID,
		credential.PublicKey,
		credential.SignCount,
		credential.AAGUID,
		nullBoolToValue(credential.BackupEligible),
		nullBoolToValue(credential.BackupState),
		credential.Transports,
		credential.CreatedAt,
		nullTimeToValue(credential.LastUsedAt),
	)
	if err != nil {
		return fmt.Errorf("upsert auth credential: %w", err)
	}

	return nil
}

// UpdateAuthCredentialSignCount updates sign counter and last-used timestamp for a credential.
func UpdateAuthCredentialSignCount(
	ctx context.Context,
	db *sql.DB,
	credentialID []byte,
	signCount uint32,
	lastUsedAt time.Time,
	backupEligible bool,
	backupState bool,
) error {
	ctx = contextOrBackground(ctx)

	_, err := db.ExecContext(
		ctx,
		`UPDATE auth_webauthn_credentials
SET sign_count = ?, last_used_at = ?, backup_eligible = ?, backup_state = ?
WHERE credential_id = ?`,
		signCount,
		lastUsedAt,
		backupEligible,
		backupState,
		credentialID,
	)
	if err != nil {
		return fmt.Errorf("update auth credential sign count: %w", err)
	}

	return nil
}

// DeleteAuthCredentialsByUser removes all credentials for a user.
func DeleteAuthCredentialsByUser(ctx context.Context, db *sql.DB, userID int64) error {
	ctx = contextOrBackground(ctx)

	_, err := db.ExecContext(ctx, `DELETE FROM auth_webauthn_credentials WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("delete auth credentials for user %d: %w", userID, err)
	}

	return nil
}

func nullBoolToValue(value sql.NullBool) any {
	if value.Valid {
		return value.Bool
	}

	return nil
}
