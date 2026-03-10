//nolint:testpackage // Store tests exercise package-internal helpers directly.
package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestAuthChallengeConsumeSingleUse(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	record := AuthChallengeRecord{
		UsedAt:        sql.NullTime{Time: time.Time{}, Valid: false},
		ChallengeID:   "challenge-1",
		Flow:          "login",
		ChallengeBlob: []byte("{}"),
		ExpiresAt:     time.Now().UTC().Add(5 * time.Minute),
		UserID:        sql.NullInt64{Int64: 0, Valid: false},
		CreatedAt:     time.Now().UTC(),
	}

	err := CreateAuthChallenge(context.Background(), db, &record)
	if err != nil {
		t.Fatalf("CreateAuthChallenge: %v", err)
	}

	_, err = ConsumeAuthChallenge(context.Background(), db, "challenge-1", "login", time.Now().UTC())
	if err != nil {
		t.Fatalf("ConsumeAuthChallenge first use: %v", err)
	}

	_, err = ConsumeAuthChallenge(context.Background(), db, "challenge-1", "login", time.Now().UTC())
	if err == nil {
		t.Fatal("expected second challenge consume to fail")
	}
}

func TestAuthSessionLifecycle(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	owner := mustCreateAuthOwner(t, db)

	now := time.Now().UTC()
	session := AuthSessionRecord{
		RevokedAt:        sql.NullTime{Time: time.Time{}, Valid: false},
		SessionID:        "session-1",
		SessionTokenHash: []byte("hash"),
		CSRFToken:        "csrf",
		UserID:           owner.ID,
		CreatedAt:        now,
		ExpiresAt:        now.Add(time.Hour),
		LastSeenAt:       now,
	}

	mustCreateAuthSession(t, db, &session)
	loaded := mustGetAuthSessionByID(t, db, "session-1")
	assertSessionCSRFToken(t, &loaded, "csrf")

	nextSeen := now.Add(15 * time.Minute)
	nextExpiry := now.Add(2 * time.Hour)

	mustTouchAuthSession(t, db, "session-1", nextSeen, nextExpiry)
	updated := mustGetAuthSessionByID(t, db, "session-1")
	assertSessionExpiry(t, &updated, nextExpiry)

	mustRevokeAuthSession(t, db, "session-1")
	revoked := mustGetAuthSessionByID(t, db, "session-1")
	assertSessionRevoked(t, &revoked)
}

func TestInitAddsAppearanceThemeToExistingAuthUsers(t *testing.T) {
	t.Parallel()

	db := openLegacyAuthSchemaDB(t)

	now := time.Now().UTC()

	_, err := db.ExecContext(
		context.Background(),
		`INSERT INTO auth_users (id, user_handle, name, display_name, created_at) VALUES (?, ?, ?, ?, ?)`,
		1,
		[]byte("owner-handle"),
		"owner",
		"Owner",
		now,
	)
	if err != nil {
		t.Fatalf("insert legacy auth owner: %v", err)
	}

	err = Init(db)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	assertHasAppearanceThemeColumn(t, db)

	owner, err := GetAuthOwner(context.Background(), db)
	if err != nil {
		t.Fatalf("GetAuthOwner: %v", err)
	}

	if owner.AppearanceTheme != AuthAppearanceThemeSystem {
		t.Fatalf("unexpected appearance theme: got %q want %q", owner.AppearanceTheme, AuthAppearanceThemeSystem)
	}
}

func mustCreateAuthOwner(t *testing.T, db *sql.DB) AuthUserRecord {
	t.Helper()

	owner, err := CreateAuthOwner(context.Background(), db, []byte("owner-handle"), "owner", "Owner")
	if err != nil {
		t.Fatalf("CreateAuthOwner: %v", err)
	}

	return owner
}

func mustCreateAuthSession(t *testing.T, db *sql.DB, session *AuthSessionRecord) {
	t.Helper()

	err := CreateAuthSession(context.Background(), db, session)
	if err != nil {
		t.Fatalf("CreateAuthSession: %v", err)
	}
}

func mustGetAuthSessionByID(t *testing.T, db *sql.DB, sessionID string) AuthSessionRecord {
	t.Helper()

	session, err := GetAuthSessionByID(context.Background(), db, sessionID)
	if err != nil {
		t.Fatalf("GetAuthSessionByID(%q): %v", sessionID, err)
	}

	return session
}

func mustTouchAuthSession(t *testing.T, db *sql.DB, sessionID string, lastSeenAt, expiresAt time.Time) {
	t.Helper()

	err := TouchAuthSession(context.Background(), db, sessionID, lastSeenAt, expiresAt)
	if err != nil {
		t.Fatalf("TouchAuthSession(%q): %v", sessionID, err)
	}
}

func mustRevokeAuthSession(t *testing.T, db *sql.DB, sessionID string) {
	t.Helper()

	err := RevokeAuthSession(context.Background(), db, sessionID)
	if err != nil {
		t.Fatalf("RevokeAuthSession(%q): %v", sessionID, err)
	}
}

func assertSessionCSRFToken(t *testing.T, session *AuthSessionRecord, want string) {
	t.Helper()

	if session.CSRFToken != want {
		t.Fatalf("unexpected csrf token: got %q want %q", session.CSRFToken, want)
	}
}

func assertSessionExpiry(t *testing.T, session *AuthSessionRecord, want time.Time) {
	t.Helper()

	if !session.ExpiresAt.Equal(want) {
		t.Fatalf("unexpected expiry: got %v want %v", session.ExpiresAt, want)
	}
}

func assertSessionRevoked(t *testing.T, session *AuthSessionRecord) {
	t.Helper()

	if !session.RevokedAt.Valid {
		t.Fatal("expected revoked session timestamp")
	}
}

func TestAuthCredentialSignCountUpdate(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	owner, err := CreateAuthOwner(context.Background(), db, []byte("owner-handle"), "owner", "Owner")
	if err != nil {
		t.Fatalf("CreateAuthOwner: %v", err)
	}

	record := AuthCredentialRecord{
		CreatedAt:      time.Now().UTC(),
		LastUsedAt:     sql.NullTime{Time: time.Time{}, Valid: false},
		Transports:     "internal",
		CredentialID:   []byte("cred-1"),
		PublicKey:      []byte("pk"),
		AAGUID:         []byte("aaguid"),
		ID:             0,
		UserID:         owner.ID,
		SignCount:      10,
		BackupEligible: sql.NullBool{Bool: false, Valid: false},
		BackupState:    sql.NullBool{Bool: false, Valid: false},
	}

	err = UpsertAuthCredential(context.Background(), db, &record)
	if err != nil {
		t.Fatalf("UpsertAuthCredential: %v", err)
	}

	err = UpdateAuthCredentialSignCount(
		context.Background(),
		db,
		[]byte("cred-1"),
		12,
		time.Now().UTC(),
		true,
		true,
	)
	if err != nil {
		t.Fatalf("UpdateAuthCredentialSignCount: %v", err)
	}

	loaded, err := GetAuthCredentialByID(context.Background(), db, []byte("cred-1"))
	if err != nil {
		t.Fatalf("GetAuthCredentialByID: %v", err)
	}

	assertCredentialUpdate(t, &loaded)
}

func TestCreateAuthOwnerDefaultsAppearanceThemeToSystem(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	owner, err := CreateAuthOwner(context.Background(), db, []byte("owner-handle"), "owner", "Owner")
	if err != nil {
		t.Fatalf("CreateAuthOwner: %v", err)
	}

	if owner.AppearanceTheme != AuthAppearanceThemeSystem {
		t.Fatalf("unexpected appearance theme: got %q want %q", owner.AppearanceTheme, AuthAppearanceThemeSystem)
	}

	theme, err := GetAuthOwnerAppearanceTheme(context.Background(), db)
	if err != nil {
		t.Fatalf("GetAuthOwnerAppearanceTheme: %v", err)
	}

	if theme != AuthAppearanceThemeSystem {
		t.Fatalf("unexpected stored appearance theme: got %q want %q", theme, AuthAppearanceThemeSystem)
	}
}

func TestUpdateAuthOwnerAppearanceThemePersistsValidThemes(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	mustCreateAuthOwner(t, db)

	validThemes := []string{
		AuthAppearanceThemeSystem,
		AuthAppearanceThemeLight,
		AuthAppearanceThemeDark,
	}

	for _, theme := range validThemes {
		err := UpdateAuthOwnerAppearanceTheme(context.Background(), db, theme)
		if err != nil {
			t.Fatalf("UpdateAuthOwnerAppearanceTheme(%q): %v", theme, err)
		}

		storedTheme, err := GetAuthOwnerAppearanceTheme(context.Background(), db)
		if err != nil {
			t.Fatalf("GetAuthOwnerAppearanceTheme: %v", err)
		}

		if storedTheme != theme {
			t.Fatalf("unexpected stored appearance theme: got %q want %q", storedTheme, theme)
		}
	}
}

func TestUpdateAuthOwnerAppearanceThemeRejectsInvalidTheme(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	mustCreateAuthOwner(t, db)

	err := UpdateAuthOwnerAppearanceTheme(context.Background(), db, AuthAppearanceThemeDark)
	if err != nil {
		t.Fatalf("UpdateAuthOwnerAppearanceTheme(%q): %v", AuthAppearanceThemeDark, err)
	}

	err = UpdateAuthOwnerAppearanceTheme(context.Background(), db, "sepia")
	if err == nil {
		t.Fatal("expected invalid appearance theme error")
	}

	if !errors.Is(err, ErrInvalidAppearanceTheme) {
		t.Fatalf("expected ErrInvalidAppearanceTheme, got %v", err)
	}

	storedTheme, err := GetAuthOwnerAppearanceTheme(context.Background(), db)
	if err != nil {
		t.Fatalf("GetAuthOwnerAppearanceTheme after invalid update: %v", err)
	}

	if storedTheme != AuthAppearanceThemeDark {
		t.Fatalf("expected invalid update to keep %q, got %q", AuthAppearanceThemeDark, storedTheme)
	}
}

func assertCredentialUpdate(t *testing.T, credential *AuthCredentialRecord) {
	t.Helper()

	if credential.SignCount != 12 {
		t.Fatalf("unexpected sign count: got %d want 12", credential.SignCount)
	}

	if !credential.LastUsedAt.Valid {
		t.Fatal("expected last_used_at timestamp")
	}

	if !credential.BackupEligible.Valid || !credential.BackupEligible.Bool {
		t.Fatal("expected backup_eligible to be true")
	}

	if !credential.BackupState.Valid || !credential.BackupState.Bool {
		t.Fatal("expected backup_state to be true")
	}
}

func TestRecoveryCodeConsume(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	err := ReplaceRecoveryCodeHash(context.Background(), db, []byte("hash-1"))
	if err != nil {
		t.Fatalf("ReplaceRecoveryCodeHash: %v", err)
	}

	present, err := HasUnusedRecoveryCode(context.Background(), db)
	if err != nil {
		t.Fatalf("HasUnusedRecoveryCode: %v", err)
	}

	if !present {
		t.Fatal("expected active recovery code")
	}

	consumed, err := ConsumeRecoveryCodeHash(context.Background(), db, []byte("hash-1"))
	if err != nil {
		t.Fatalf("ConsumeRecoveryCodeHash: %v", err)
	}

	if !consumed {
		t.Fatal("expected code to be consumed")
	}

	again, err := ConsumeRecoveryCodeHash(context.Background(), db, []byte("hash-1"))
	if err != nil {
		t.Fatalf("ConsumeRecoveryCodeHash second: %v", err)
	}

	if again {
		t.Fatal("expected consumed code to be single-use")
	}
}

func TestGetAuthOwnerMissing(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	_, err := GetAuthOwner(context.Background(), db)
	if err == nil {
		t.Fatal("expected missing owner error")
	}

	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func openLegacyAuthSchemaDB(t *testing.T) *sql.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "legacy-auth.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() {
		closeErr := db.Close()
		if closeErr != nil {
			t.Errorf("db.Close: %v", closeErr)
		}
	})

	_, err = db.ExecContext(context.Background(), `
CREATE TABLE auth_users (
	id INTEGER PRIMARY KEY,
	user_handle BLOB NOT NULL UNIQUE,
	name TEXT NOT NULL,
	display_name TEXT NOT NULL,
	created_at DATETIME NOT NULL
)
`)
	if err != nil {
		t.Fatalf("create legacy auth_users table: %v", err)
	}

	return db
}

func assertHasAppearanceThemeColumn(t *testing.T, db *sql.DB) {
	t.Helper()

	var hasAppearanceTheme int

	err := db.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM pragma_table_info('auth_users') WHERE name = 'appearance_theme'`,
	).Scan(&hasAppearanceTheme)
	if err != nil {
		t.Fatalf("check appearance_theme column: %v", err)
	}

	if hasAppearanceTheme != 1 {
		t.Fatal("expected appearance_theme column to be added")
	}
}
