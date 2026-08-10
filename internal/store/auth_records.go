package store

import (
	"database/sql"
	"errors"
	"time"
)

// AuthUserRecord stores the single app user's WebAuthn identity data.
type AuthUserRecord struct {
	CreatedAt       time.Time
	Name            string
	DisplayName     string
	AppearanceTheme string
	UserHandle      []byte
	ID              int64
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
	ClientKey     string
	ChallengeBlob []byte
	UserID        sql.NullInt64
}

// ErrAuthChallengeMissing indicates the challenge was missing, expired, or already consumed.
var (
	ErrAuthChallengeMissing = errors.New("auth challenge not found")
	// ErrInvalidAppearanceTheme indicates an unsupported auth owner theme preference.
	ErrInvalidAppearanceTheme          = errors.New("invalid appearance theme")
	errUnsupportedAuthCredentialColumn = errors.New("unsupported auth credential column")
	errInvalidAuthCredentialSignCount  = errors.New("invalid auth credential sign count")
)

const (
	maxAuthChallengesGlobal    = 256
	maxAuthChallengesPerFlow   = 128
	maxAuthChallengesPerClient = 4
	// AuthAppearanceThemeSystem follows the active OS/browser theme preference.
	AuthAppearanceThemeSystem = "system"
	// AuthAppearanceThemeLight forces the light theme.
	AuthAppearanceThemeLight = "light"
	// AuthAppearanceThemeDark forces the dark theme.
	AuthAppearanceThemeDark = "dark"
)
