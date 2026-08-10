package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

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
