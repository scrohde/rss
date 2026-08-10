package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// GetAuthOwner returns the singleton owner record.
func GetAuthOwner(ctx context.Context, db *sql.DB) (AuthUserRecord, error) {
	ctx = contextOrBackground(ctx)

	user, err := scanAuthUserRow(db.QueryRowContext(
		ctx,
		`SELECT id, user_handle, name, display_name, appearance_theme, created_at FROM auth_users WHERE id = 1`,
	))
	if err != nil {
		return AuthUserRecord{}, fmt.Errorf("load auth owner: %w", err)
	}

	return user, nil
}

// GetAuthUserByID looks up an auth user by numeric ID.
func GetAuthUserByID(ctx context.Context, db *sql.DB, userID int64) (AuthUserRecord, error) {
	ctx = contextOrBackground(ctx)

	user, err := scanAuthUserRow(db.QueryRowContext(
		ctx,
		`SELECT id, user_handle, name, display_name, appearance_theme, created_at FROM auth_users WHERE id = ?`,
		userID,
	))
	if err != nil {
		return AuthUserRecord{}, fmt.Errorf("load auth user %d: %w", userID, err)
	}

	return user, nil
}

// GetAuthUserByHandle looks up an auth user by WebAuthn user handle.
func GetAuthUserByHandle(ctx context.Context, db *sql.DB, handle []byte) (AuthUserRecord, error) {
	ctx = contextOrBackground(ctx)

	user, err := scanAuthUserRow(db.QueryRowContext(
		ctx,
		`SELECT id, user_handle, name, display_name, appearance_theme, created_at
FROM auth_users WHERE user_handle = ?`,
		handle,
	))
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

// GetAuthOwnerAppearanceTheme loads the persisted appearance theme for the singleton owner.
func GetAuthOwnerAppearanceTheme(ctx context.Context, db *sql.DB) (string, error) {
	owner, err := GetAuthOwner(ctx, db)
	if err != nil {
		return "", err
	}

	return owner.AppearanceTheme, nil
}

// UpdateAuthOwnerAppearanceTheme persists the owner appearance theme after validating the value.
func UpdateAuthOwnerAppearanceTheme(ctx context.Context, db *sql.DB, theme string) error {
	ctx = contextOrBackground(ctx)

	validatedTheme, err := validateAuthAppearanceTheme(theme)
	if err != nil {
		return err
	}

	result, err := db.ExecContext(
		ctx,
		`UPDATE auth_users SET appearance_theme = ? WHERE id = 1`,
		validatedTheme,
	)
	if err != nil {
		return fmt.Errorf("update auth owner appearance theme: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated auth owner appearance theme rows: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("update auth owner appearance theme: %w", sql.ErrNoRows)
	}

	return nil
}

func scanAuthUserRow(scanner interface {
	Scan(dest ...any) error
},
) (AuthUserRecord, error) {
	var user AuthUserRecord

	err := scanner.Scan(
		&user.ID,
		&user.UserHandle,
		&user.Name,
		&user.DisplayName,
		&user.AppearanceTheme,
		&user.CreatedAt,
	)
	if err != nil {
		return AuthUserRecord{}, fmt.Errorf("scan auth user row: %w", err)
	}

	return user, nil
}

func validateAuthAppearanceTheme(theme string) (string, error) {
	switch theme {
	case AuthAppearanceThemeSystem, AuthAppearanceThemeLight, AuthAppearanceThemeDark:
		return theme, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidAppearanceTheme, theme)
	}
}
