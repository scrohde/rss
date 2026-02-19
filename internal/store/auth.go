package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

// AuthUserRecord stores the single app user's WebAuthn identity data.
type AuthUserRecord struct {
	CreatedAt   time.Time
	Name        string
	DisplayName string
	UserHandle  []byte
	ID          int64
}

// AuthCredentialRecord stores a registered WebAuthn credential.
type AuthCredentialRecord struct {
	CreatedAt      time.Time
	LastUsedAt     sql.NullTime
	Transports     string
	CredentialID   []byte
	PublicKey      []byte
	AAGUID         []byte
	ID             int64
	UserID         int64
	SignCount      uint32
	BackupEligible sql.NullBool
	BackupState    sql.NullBool
}

// AuthSessionRecord stores an authenticated browser session.
type AuthSessionRecord struct {
	CreatedAt        time.Time
	ExpiresAt        time.Time
	LastSeenAt       time.Time
	RevokedAt        sql.NullTime
	SessionID        string
	CSRFToken        string
	SessionTokenHash []byte
	UserID           int64
}

// AuthChallengeRecord stores short-lived WebAuthn ceremony session data.
type AuthChallengeRecord struct {
	ExpiresAt     time.Time
	CreatedAt     time.Time
	UsedAt        sql.NullTime
	ChallengeID   string
	Flow          string
	ChallengeBlob []byte
	UserID        sql.NullInt64
}

// ErrAuthChallengeMissing indicates the challenge was missing, expired, or already consumed.
var (
	ErrAuthChallengeMissing            = errors.New("auth challenge not found")
	errUnsupportedAuthCredentialColumn = errors.New("unsupported auth credential column")
	errInvalidAuthCredentialSignCount  = errors.New("invalid auth credential sign count")
)

const authSchemaSQL = `
CREATE TABLE IF NOT EXISTS auth_users (
	id INTEGER PRIMARY KEY,
	user_handle BLOB NOT NULL UNIQUE,
	name TEXT NOT NULL,
	display_name TEXT NOT NULL,
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

	err = ensureAuthCredentialFlagColumn(db, "backup_eligible")
	if err != nil {
		return err
	}

	err = ensureAuthCredentialFlagColumn(db, "backup_state")
	if err != nil {
		return err
	}

	return nil
}

func ensureAuthCredentialFlagColumn(db *sql.DB, column string) error {
	var count int

	err := db.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM pragma_table_info('auth_webauthn_credentials') WHERE name = ?`,
		column,
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check auth credential column %q: %w", column, err)
	}

	if count > 0 {
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

// GetAuthOwner returns the singleton owner record.
func GetAuthOwner(ctx context.Context, db *sql.DB) (AuthUserRecord, error) {
	ctx = contextOrBackground(ctx)

	var user AuthUserRecord

	err := db.QueryRowContext(
		ctx,
		`SELECT id, user_handle, name, display_name, created_at FROM auth_users WHERE id = 1`,
	).Scan(&user.ID, &user.UserHandle, &user.Name, &user.DisplayName, &user.CreatedAt)
	if err != nil {
		return AuthUserRecord{}, fmt.Errorf("load auth owner: %w", err)
	}

	return user, nil
}

// GetAuthUserByID looks up an auth user by numeric ID.
func GetAuthUserByID(ctx context.Context, db *sql.DB, userID int64) (AuthUserRecord, error) {
	ctx = contextOrBackground(ctx)

	var user AuthUserRecord

	err := db.QueryRowContext(
		ctx,
		`SELECT id, user_handle, name, display_name, created_at FROM auth_users WHERE id = ?`,
		userID,
	).Scan(&user.ID, &user.UserHandle, &user.Name, &user.DisplayName, &user.CreatedAt)
	if err != nil {
		return AuthUserRecord{}, fmt.Errorf("load auth user %d: %w", userID, err)
	}

	return user, nil
}

// GetAuthUserByHandle looks up an auth user by WebAuthn user handle.
func GetAuthUserByHandle(ctx context.Context, db *sql.DB, handle []byte) (AuthUserRecord, error) {
	ctx = contextOrBackground(ctx)

	var user AuthUserRecord

	err := db.QueryRowContext(
		ctx,
		`SELECT id, user_handle, name, display_name, created_at FROM auth_users WHERE user_handle = ?`,
		handle,
	).Scan(&user.ID, &user.UserHandle, &user.Name, &user.DisplayName, &user.CreatedAt)
	if err != nil {
		return AuthUserRecord{}, fmt.Errorf("load auth user by handle: %w", err)
	}

	return user, nil
}

// CreateAuthOwner inserts the singleton owner row if it does not already exist.
func CreateAuthOwner(ctx context.Context, db *sql.DB, handle []byte, name, displayName string) (AuthUserRecord, error) {
	ctx = contextOrBackground(ctx)

	now := time.Now().UTC()

	_, err := db.ExecContext(ctx, `
INSERT OR IGNORE INTO auth_users (id, user_handle, name, display_name, created_at)
VALUES (1, ?, ?, ?, ?)
	`, handle, name, displayName, now)
	if err != nil {
		return AuthUserRecord{}, fmt.Errorf("create auth owner: %w", err)
	}

	owner, err := GetAuthOwner(ctx, db)
	if err != nil {
		return AuthUserRecord{}, err
	}

	return owner, nil
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

// CreateAuthSession inserts an authenticated session row.
func CreateAuthSession(ctx context.Context, db *sql.DB, session *AuthSessionRecord) error {
	ctx = contextOrBackground(ctx)

	_, err := db.ExecContext(ctx, `
INSERT INTO auth_sessions
(session_id, session_token_hash, csrf_token, user_id, created_at, expires_at, last_seen_at, revoked_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		session.SessionID,
		session.SessionTokenHash,
		session.CSRFToken,
		session.UserID,
		session.CreatedAt,
		session.ExpiresAt,
		session.LastSeenAt,
		nullTimeToValue(session.RevokedAt),
	)
	if err != nil {
		return fmt.Errorf("create auth session: %w", err)
	}

	return nil
}

// GetAuthSessionByID retrieves an auth session by ID.
func GetAuthSessionByID(ctx context.Context, db *sql.DB, sessionID string) (AuthSessionRecord, error) {
	ctx = contextOrBackground(ctx)

	var session AuthSessionRecord

	err := db.QueryRowContext(ctx, `
SELECT session_id, session_token_hash, csrf_token, user_id, created_at, expires_at, last_seen_at, revoked_at
FROM auth_sessions
WHERE session_id = ?
	`, sessionID).Scan(
		&session.SessionID,
		&session.SessionTokenHash,
		&session.CSRFToken,
		&session.UserID,
		&session.CreatedAt,
		&session.ExpiresAt,
		&session.LastSeenAt,
		&session.RevokedAt,
	)
	if err != nil {
		return AuthSessionRecord{}, fmt.Errorf("load auth session %q: %w", sessionID, err)
	}

	return session, nil
}

// TouchAuthSession updates rolling session activity timestamps.
func TouchAuthSession(ctx context.Context, db *sql.DB, sessionID string, lastSeenAt, expiresAt time.Time) error {
	ctx = contextOrBackground(ctx)

	_, err := db.ExecContext(
		ctx,
		`UPDATE auth_sessions SET last_seen_at = ?, expires_at = ? WHERE session_id = ?`,
		lastSeenAt,
		expiresAt,
		sessionID,
	)
	if err != nil {
		return fmt.Errorf("touch auth session %q: %w", sessionID, err)
	}

	return nil
}

// RevokeAuthSession revokes a specific session.
func RevokeAuthSession(ctx context.Context, db *sql.DB, sessionID string) error {
	ctx = contextOrBackground(ctx)

	_, err := db.ExecContext(
		ctx,
		`UPDATE auth_sessions SET revoked_at = ? WHERE session_id = ? AND revoked_at IS NULL`,
		time.Now().UTC(),
		sessionID,
	)
	if err != nil {
		return fmt.Errorf("revoke auth session %q: %w", sessionID, err)
	}

	return nil
}

// RevokeAllAuthSessions revokes all active sessions for a user.
func RevokeAllAuthSessions(ctx context.Context, db *sql.DB, userID int64) error {
	ctx = contextOrBackground(ctx)

	_, err := db.ExecContext(
		ctx,
		`UPDATE auth_sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`,
		time.Now().UTC(),
		userID,
	)
	if err != nil {
		return fmt.Errorf("revoke all auth sessions for user %d: %w", userID, err)
	}

	return nil
}

// DeleteExpiredAuthSessions removes stale session rows.
func DeleteExpiredAuthSessions(ctx context.Context, db *sql.DB, now time.Time) error {
	ctx = contextOrBackground(ctx)

	_, err := db.ExecContext(
		ctx,
		`DELETE FROM auth_sessions WHERE expires_at <= ? OR (revoked_at IS NOT NULL AND revoked_at <= ?)`,
		now,
		now.Add(-24*time.Hour),
	)
	if err != nil {
		return fmt.Errorf("delete expired auth sessions: %w", err)
	}

	return nil
}

// CreateAuthChallenge stores WebAuthn ceremony session data.
func CreateAuthChallenge(ctx context.Context, db *sql.DB, challenge *AuthChallengeRecord) error {
	ctx = contextOrBackground(ctx)

	_, err := db.ExecContext(ctx, `
INSERT INTO auth_webauthn_challenges
(challenge_id, flow, challenge_blob, expires_at, used_at, user_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
	`,
		challenge.ChallengeID,
		challenge.Flow,
		challenge.ChallengeBlob,
		challenge.ExpiresAt,
		nullTimeToValue(challenge.UsedAt),
		nullInt64ToValue(challenge.UserID),
		challenge.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create auth challenge: %w", err)
	}

	return nil
}

// ConsumeAuthChallenge atomically marks a challenge as used and returns it.
func ConsumeAuthChallenge(
	ctx context.Context,
	db *sql.DB,
	challengeID string,
	flow string,
	now time.Time,
) (AuthChallengeRecord, error) {
	ctx = contextOrBackground(ctx)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return AuthChallengeRecord{}, fmt.Errorf("begin consume auth challenge transaction: %w", err)
	}

	challenge, err := consumeAuthChallengeTx(ctx, tx, challengeID, flow, now)
	if err != nil {
		rollbackTx(tx)

		return AuthChallengeRecord{}, err
	}

	err = tx.Commit()
	if err != nil {
		return AuthChallengeRecord{}, fmt.Errorf("commit consume auth challenge transaction: %w", err)
	}

	return challenge, nil
}

func consumeAuthChallengeTx(
	ctx context.Context,
	tx *sql.Tx,
	challengeID string,
	flow string,
	now time.Time,
) (AuthChallengeRecord, error) {
	challenge, err := queryAuthChallenge(ctx, tx, challengeID, flow)
	if err != nil {
		return AuthChallengeRecord{}, err
	}

	if !authChallengeAvailable(&challenge, now) {
		return AuthChallengeRecord{}, ErrAuthChallengeMissing
	}

	updated, err := markAuthChallengeUsed(ctx, tx, challengeID, now)
	if err != nil {
		return AuthChallengeRecord{}, err
	}

	if !updated {
		return AuthChallengeRecord{}, ErrAuthChallengeMissing
	}

	challenge.UsedAt = sql.NullTime{Time: now, Valid: true}

	return challenge, nil
}

func queryAuthChallenge(
	ctx context.Context,
	tx *sql.Tx,
	challengeID string,
	flow string,
) (AuthChallengeRecord, error) {
	var challenge AuthChallengeRecord

	err := tx.QueryRowContext(ctx, `
SELECT challenge_id, flow, challenge_blob, expires_at, used_at, user_id, created_at
FROM auth_webauthn_challenges
WHERE challenge_id = ? AND flow = ?
	`, challengeID, flow).Scan(
		&challenge.ChallengeID,
		&challenge.Flow,
		&challenge.ChallengeBlob,
		&challenge.ExpiresAt,
		&challenge.UsedAt,
		&challenge.UserID,
		&challenge.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AuthChallengeRecord{}, ErrAuthChallengeMissing
		}

		return AuthChallengeRecord{}, fmt.Errorf("load auth challenge: %w", err)
	}

	return challenge, nil
}

func authChallengeAvailable(challenge *AuthChallengeRecord, now time.Time) bool {
	return !challenge.UsedAt.Valid && challenge.ExpiresAt.After(now)
}

func markAuthChallengeUsed(
	ctx context.Context,
	tx *sql.Tx,
	challengeID string,
	now time.Time,
) (bool, error) {
	result, err := tx.ExecContext(
		ctx,
		`UPDATE auth_webauthn_challenges SET used_at = ? WHERE challenge_id = ? AND used_at IS NULL`,
		now,
		challengeID,
	)
	if err != nil {
		return false, fmt.Errorf("mark auth challenge used: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count auth challenge updates: %w", err)
	}

	return affected == 1, nil
}

// DeleteExpiredAuthChallenges removes stale challenge rows.
func DeleteExpiredAuthChallenges(ctx context.Context, db *sql.DB, now time.Time) error {
	ctx = contextOrBackground(ctx)

	_, err := db.ExecContext(
		ctx,
		`DELETE FROM auth_webauthn_challenges WHERE expires_at <= ? OR used_at IS NOT NULL`,
		now,
	)
	if err != nil {
		return fmt.Errorf("delete expired auth challenges: %w", err)
	}

	return nil
}

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

func nullInt64ToValue(value sql.NullInt64) any {
	if value.Valid {
		return value.Int64
	}

	return nil
}

func nullBoolToValue(value sql.NullBool) any {
	if value.Valid {
		return value.Bool
	}

	return nil
}
