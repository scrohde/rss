package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CreateAuthChallenge stores WebAuthn ceremony session data.
func CreateAuthChallenge(ctx context.Context, db *sql.DB, challenge *AuthChallengeRecord) error {
	ctx = contextOrBackground(ctx)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin create auth challenge transaction: %w", err)
	}
	defer rollbackTx(tx)

	_, err = tx.ExecContext(ctx, `DELETE FROM auth_webauthn_challenges WHERE expires_at <= ? OR used_at IS NOT NULL`,
		challenge.CreatedAt)
	if err != nil {
		return fmt.Errorf("clean auth challenges: %w", err)
	}

	_, err = tx.ExecContext(ctx, `DELETE FROM auth_webauthn_challenges WHERE challenge_id IN (
		SELECT challenge_id FROM auth_webauthn_challenges WHERE flow = ? AND client_key = ?
		ORDER BY created_at DESC LIMIT -1 OFFSET ?)`, challenge.Flow, challenge.ClientKey, maxAuthChallengesPerClient-1)
	if err != nil {
		return fmt.Errorf("bound client auth challenges: %w", err)
	}

	_, err = tx.ExecContext(ctx, `DELETE FROM auth_webauthn_challenges WHERE challenge_id IN (
		SELECT challenge_id FROM auth_webauthn_challenges WHERE flow = ?
		ORDER BY created_at DESC LIMIT -1 OFFSET ?)`, challenge.Flow, maxAuthChallengesPerFlow-1)
	if err != nil {
		return fmt.Errorf("bound flow auth challenges: %w", err)
	}

	_, err = tx.ExecContext(ctx, `DELETE FROM auth_webauthn_challenges WHERE challenge_id IN (
		SELECT challenge_id FROM auth_webauthn_challenges ORDER BY created_at DESC LIMIT -1 OFFSET ?)`,
		maxAuthChallengesGlobal-1)
	if err != nil {
		return fmt.Errorf("bound global auth challenges: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO auth_webauthn_challenges
(challenge_id, flow, client_key, challenge_blob, expires_at, used_at, user_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		challenge.ChallengeID,
		challenge.Flow,
		challenge.ClientKey,
		challenge.ChallengeBlob,
		challenge.ExpiresAt,
		nullTimeToValue(challenge.UsedAt),
		nullInt64ToValue(challenge.UserID),
		challenge.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create auth challenge: %w", err)
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("commit create auth challenge: %w", err)
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

func nullInt64ToValue(value sql.NullInt64) any {
	if value.Valid {
		return value.Int64
	}

	return nil
}
