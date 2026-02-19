// Package auth provides passkey authentication and session management.
//
//nolint:revive // The auth package intentionally exports multiple DTO structs for cross-package API boundaries.
package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"rss/internal/store"
)

const (
	challengeFlowLogin    = "login"
	challengeFlowRegister = "register"

	defaultSessionTTL   = 24 * time.Hour
	defaultChallengeTTL = 5 * time.Minute
	ownerHandleBytes    = 32
	sessionTokenBytes   = 32
	csrfTokenBytes      = 32
	recoveryTokenBytes  = 24
	challengeIDBytes    = 24
	sessionIDTokenBytes = 24
)

var (
	// ErrInvalidSession indicates the provided session cookie did not match an active session.
	ErrInvalidSession = errors.New("invalid auth session")
	// ErrChallengeNotFound indicates the challenge was missing, expired, or already used.
	ErrChallengeNotFound                  = errors.New("auth challenge not found")
	errConfigMissingRPID                  = errors.New("auth config missing RPID")
	errConfigMissingRPOrigin              = errors.New("auth config missing RPOrigin")
	errInvalidPasskeyUserType             = errors.New("invalid passkey user type")
	errMissingPasskeyCredentialID         = errors.New("passkey assertion missing credential id")
	errRegistrationChallengeMissingUserID = errors.New("registration challenge missing user id")
)

// Config controls the passkey authentication service.
type Config struct {
	RPID         string
	RPOrigin     string
	RPName       string
	CookieName   string
	SessionTTL   time.Duration
	ChallengeTTL time.Duration
	CookieSecure bool
}

// SessionPrincipal is a validated authenticated session.
type SessionPrincipal struct {
	SessionID string
	CSRFToken string
	UserID    int64
}

// SessionIssue represents a newly minted browser session token.
type SessionIssue struct {
	SessionID   string
	CookieValue string
	CSRFToken   string
	UserID      int64
}

// LoginBeginResult contains WebAuthn options plus challenge handle.
type LoginBeginResult struct {
	Assertion   *protocol.CredentialAssertion
	ChallengeID string
}

// RegistrationBeginResult contains WebAuthn registration options plus challenge handle.
type RegistrationBeginResult struct {
	Creation    *protocol.CredentialCreation
	ChallengeID string
}

// Manager encapsulates passkey/auth session operations.
type Manager struct {
	db           *sql.DB
	webauthn     *webauthn.WebAuthn
	sessionTTL   time.Duration
	challengeTTL time.Duration
}

// NewManager creates a passkey auth manager.
func NewManager(db *sql.DB, cfg *Config) (*Manager, error) {
	if cfg == nil {
		return nil, errConfigMissingRPID
	}

	if strings.TrimSpace(cfg.RPName) == "" {
		cfg.RPName = "Pulse RSS"
	}

	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = defaultSessionTTL
	}

	if cfg.ChallengeTTL <= 0 {
		cfg.ChallengeTTL = defaultChallengeTTL
	}

	if strings.TrimSpace(cfg.RPID) == "" {
		return nil, errConfigMissingRPID
	}

	if strings.TrimSpace(cfg.RPOrigin) == "" {
		return nil, errConfigMissingRPOrigin
	}

	selection := protocol.AuthenticatorSelection{
		AuthenticatorAttachment: "",
		RequireResidentKey:      protocol.ResidentKeyRequired(),
		ResidentKey:             protocol.ResidentKeyRequirementRequired,
		UserVerification:        protocol.VerificationRequired,
	}

	webAuthnConfig := new(webauthn.Config)
	webAuthnConfig.RPID = cfg.RPID
	webAuthnConfig.RPDisplayName = cfg.RPName
	webAuthnConfig.RPOrigins = []string{cfg.RPOrigin}
	webAuthnConfig.AttestationPreference = protocol.PreferNoAttestation
	webAuthnConfig.AuthenticatorSelection = selection

	webAuthn, err := webauthn.New(webAuthnConfig)
	if err != nil {
		return nil, fmt.Errorf("initialize webauthn: %w", err)
	}

	return &Manager{
		db:           db,
		webauthn:     webAuthn,
		sessionTTL:   cfg.SessionTTL,
		challengeTTL: cfg.ChallengeTTL,
	}, nil
}

// CredentialCount returns the registered passkey count.
func (m *Manager) CredentialCount(ctx context.Context) (int, error) {
	count, err := store.AuthCredentialCount(ctx, m.db)
	if err != nil {
		return 0, fmt.Errorf("count auth credentials: %w", err)
	}

	return count, nil
}

// EnsureOwner ensures the singleton owner identity exists.
func (m *Manager) EnsureOwner(ctx context.Context) (store.AuthUserRecord, error) {
	owner, err := store.GetAuthOwner(ctx, m.db)
	if err == nil {
		return owner, nil
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return store.AuthUserRecord{}, fmt.Errorf("load auth owner: %w", err)
	}

	handle, err := randomBytes(ownerHandleBytes)
	if err != nil {
		return store.AuthUserRecord{}, fmt.Errorf("generate owner handle: %w", err)
	}

	owner, err = store.CreateAuthOwner(ctx, m.db, handle, "owner", "Pulse RSS Owner")
	if err != nil {
		return store.AuthUserRecord{}, fmt.Errorf("create auth owner: %w", err)
	}

	return owner, nil
}

// BeginDiscoverableLogin starts a username-less passkey login ceremony.
func (m *Manager) BeginDiscoverableLogin(ctx context.Context) (LoginBeginResult, error) {
	assertion, sessionData, err := m.webauthn.BeginDiscoverableLogin(
		webauthn.WithUserVerification(protocol.VerificationRequired),
	)
	if err != nil {
		return LoginBeginResult{}, fmt.Errorf("begin discoverable login: %w", err)
	}

	challengeID, err := m.storeChallenge(
		ctx,
		challengeFlowLogin,
		sql.NullInt64{Int64: 0, Valid: false},
		sessionData,
	)
	if err != nil {
		return LoginBeginResult{}, err
	}

	return LoginBeginResult{ChallengeID: challengeID, Assertion: assertion}, nil
}

// FinishDiscoverableLogin verifies a discoverable passkey assertion and creates a session.
func (m *Manager) FinishDiscoverableLogin(
	ctx context.Context,
	challengeID string,
	r *http.Request,
) (SessionIssue, error) {
	sessionData, _, err := m.consumeChallenge(ctx, challengeID, challengeFlowLogin)
	if err != nil {
		return SessionIssue{}, err
	}

	parsed, err := protocol.ParseCredentialRequestResponse(r)
	if err != nil {
		return SessionIssue{}, fmt.Errorf("parse passkey assertion: %w", err)
	}

	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		user, loadErr := m.resolveLoginUser(ctx, rawID, userHandle)
		if loadErr != nil {
			return nil, loadErr
		}

		user.prepareCredentialForAssertion(
			rawID,
			parsed.Response.AuthenticatorData.Flags.HasBackupEligible(),
			parsed.Response.AuthenticatorData.Flags.HasBackupState(),
		)

		return user, nil
	}

	resolvedUser, credential, err := m.webauthn.ValidatePasskeyLogin(handler, sessionData, parsed)
	if err != nil {
		return SessionIssue{}, fmt.Errorf("validate passkey login: %w", err)
	}

	user, ok := resolvedUser.(*appUser)
	if !ok {
		return SessionIssue{}, errInvalidPasskeyUserType
	}

	now := time.Now().UTC()

	updateErr := store.UpdateAuthCredentialSignCount(
		ctx,
		m.db,
		credential.ID,
		credential.Authenticator.SignCount,
		now,
		credential.Flags.BackupEligible,
		credential.Flags.BackupState,
	)
	if updateErr != nil {
		return SessionIssue{}, fmt.Errorf("update credential sign count: %w", updateErr)
	}

	return m.createSession(ctx, user.id, now)
}

// BeginRegistration starts a passkey registration ceremony for a known user.
func (m *Manager) BeginRegistration(ctx context.Context, userID int64) (RegistrationBeginResult, error) {
	user, err := m.loadUserByID(ctx, userID)
	if err != nil {
		return RegistrationBeginResult{}, err
	}

	exclusions := user.credentialDescriptors()

	creation, sessionData, err := m.webauthn.BeginRegistration(
		user,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			AuthenticatorAttachment: "",
			RequireResidentKey:      protocol.ResidentKeyRequired(),
			ResidentKey:             protocol.ResidentKeyRequirementRequired,
			UserVerification:        protocol.VerificationRequired,
		}),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
		webauthn.WithExclusions(exclusions),
	)
	if err != nil {
		return RegistrationBeginResult{}, fmt.Errorf("begin registration: %w", err)
	}

	challengeID, err := m.storeChallenge(
		ctx,
		challengeFlowRegister,
		sql.NullInt64{Int64: user.id, Valid: true},
		sessionData,
	)
	if err != nil {
		return RegistrationBeginResult{}, err
	}

	return RegistrationBeginResult{ChallengeID: challengeID, Creation: creation}, nil
}

// FinishRegistration verifies a passkey registration ceremony.
func (m *Manager) FinishRegistration(
	ctx context.Context,
	challengeID string,
	r *http.Request,
) (int64, error) {
	sessionData, challenge, err := m.consumeChallenge(ctx, challengeID, challengeFlowRegister)
	if err != nil {
		return 0, err
	}

	if !challenge.UserID.Valid {
		return 0, errRegistrationChallengeMissingUserID
	}

	user, err := m.loadUserByID(ctx, challenge.UserID.Int64)
	if err != nil {
		return 0, err
	}

	credential, err := m.webauthn.FinishRegistration(user, sessionData, r)
	if err != nil {
		return 0, fmt.Errorf("finish registration: %w", err)
	}

	record := store.AuthCredentialRecord{
		ID:             0,
		UserID:         user.id,
		CredentialID:   credential.ID,
		PublicKey:      credential.PublicKey,
		SignCount:      credential.Authenticator.SignCount,
		AAGUID:         credential.Authenticator.AAGUID,
		LastUsedAt:     sql.NullTime{Time: time.Time{}, Valid: false},
		BackupEligible: sql.NullBool{Bool: credential.Flags.BackupEligible, Valid: true},
		BackupState:    sql.NullBool{Bool: credential.Flags.BackupState, Valid: true},
		Transports:     joinTransports(credential.Transport),
		CreatedAt:      time.Now().UTC(),
	}

	upsertErr := store.UpsertAuthCredential(ctx, m.db, &record)
	if upsertErr != nil {
		return 0, fmt.Errorf("store passkey credential: %w", upsertErr)
	}

	return user.id, nil
}

// CreateSessionForUser creates a new authenticated browser session.
func (m *Manager) CreateSessionForUser(ctx context.Context, userID int64) (SessionIssue, error) {
	return m.createSession(ctx, userID, time.Now().UTC())
}

// ValidateSessionCookie validates and rolls forward an active session.
func (m *Manager) ValidateSessionCookie(ctx context.Context, cookieValue string) (SessionPrincipal, error) {
	sessionID, token, ok := parseSessionCookie(cookieValue)
	if !ok {
		return SessionPrincipal{}, ErrInvalidSession
	}

	record, err := store.GetAuthSessionByID(ctx, m.db, sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionPrincipal{}, ErrInvalidSession
		}

		return SessionPrincipal{}, fmt.Errorf("load auth session %q: %w", sessionID, err)
	}

	if record.RevokedAt.Valid || !record.ExpiresAt.After(time.Now().UTC()) {
		return SessionPrincipal{}, ErrInvalidSession
	}

	incoming := sha256Bytes([]byte(token))
	if subtle.ConstantTimeCompare(incoming, record.SessionTokenHash) != 1 {
		return SessionPrincipal{}, ErrInvalidSession
	}

	now := time.Now().UTC()

	touchErr := store.TouchAuthSession(ctx, m.db, sessionID, now, now.Add(m.sessionTTL))
	if touchErr != nil {
		return SessionPrincipal{}, fmt.Errorf("touch auth session %q: %w", sessionID, touchErr)
	}

	return SessionPrincipal{
		SessionID: record.SessionID,
		UserID:    record.UserID,
		CSRFToken: record.CSRFToken,
	}, nil
}

// RevokeSessionCookie revokes the session identified by a cookie value.
func (m *Manager) RevokeSessionCookie(ctx context.Context, cookieValue string) error {
	sessionID, _, ok := parseSessionCookie(cookieValue)
	if !ok {
		return nil
	}

	err := store.RevokeAuthSession(ctx, m.db, sessionID)
	if err != nil {
		return fmt.Errorf("revoke auth session %q: %w", sessionID, err)
	}

	return nil
}

// RotateSession revokes an old session and issues a new one.
func (m *Manager) RotateSession(ctx context.Context, oldCookieValue string, userID int64) (SessionIssue, error) {
	err := m.RevokeSessionCookie(ctx, oldCookieValue)
	if err != nil {
		return SessionIssue{}, err
	}

	return m.CreateSessionForUser(ctx, userID)
}

// GenerateRecoveryCode issues a new single-use recovery code.
func (m *Manager) GenerateRecoveryCode(ctx context.Context) (string, error) {
	raw, err := randomToken(recoveryTokenBytes)
	if err != nil {
		return "", fmt.Errorf("generate recovery code: %w", err)
	}

	code := normalizeRecoveryCode(raw)

	err = store.ReplaceRecoveryCodeHash(ctx, m.db, sha256Bytes([]byte(code)))
	if err != nil {
		return "", fmt.Errorf("store recovery code hash: %w", err)
	}

	return code, nil
}

// HasRecoveryCode returns true when a code is active.
func (m *Manager) HasRecoveryCode(ctx context.Context) (bool, error) {
	present, err := store.HasUnusedRecoveryCode(ctx, m.db)
	if err != nil {
		return false, fmt.Errorf("load recovery code state: %w", err)
	}

	return present, nil
}

// ConsumeRecoveryCode validates and consumes a recovery code.
func (m *Manager) ConsumeRecoveryCode(ctx context.Context, code string) (bool, error) {
	normalized := normalizeRecoveryCode(code)
	if normalized == "" {
		return false, nil
	}

	consumed, err := store.ConsumeRecoveryCodeHash(ctx, m.db, sha256Bytes([]byte(normalized)))
	if err != nil {
		return false, fmt.Errorf("consume recovery code hash: %w", err)
	}

	if !consumed {
		return false, nil
	}

	owner, err := store.GetAuthOwner(ctx, m.db)
	if err != nil {
		return false, fmt.Errorf("load auth owner: %w", err)
	}

	err = store.DeleteAuthCredentialsByUser(ctx, m.db, owner.ID)
	if err != nil {
		return false, fmt.Errorf("delete auth credentials for user %d: %w", owner.ID, err)
	}

	err = store.RevokeAllAuthSessions(ctx, m.db, owner.ID)
	if err != nil {
		return false, fmt.Errorf("revoke auth sessions for user %d: %w", owner.ID, err)
	}

	return true, nil
}

// CleanupExpiredAuthData removes stale auth challenges/sessions.
func (m *Manager) CleanupExpiredAuthData(ctx context.Context) error {
	now := time.Now().UTC()

	err := store.DeleteExpiredAuthChallenges(ctx, m.db, now)
	if err != nil {
		return fmt.Errorf("delete expired auth challenges: %w", err)
	}

	err = store.DeleteExpiredAuthSessions(ctx, m.db, now)
	if err != nil {
		return fmt.Errorf("delete expired auth sessions: %w", err)
	}

	return nil
}

func (m *Manager) resolveLoginUser(ctx context.Context, rawID, userHandle []byte) (*appUser, error) {
	if len(userHandle) > 0 {
		user, err := m.loadUserByHandle(ctx, userHandle)
		if err == nil {
			return user, nil
		}

		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("load auth user by handle: %w", err)
		}
	}

	if len(rawID) == 0 {
		return nil, errMissingPasskeyCredentialID
	}

	credential, err := store.GetAuthCredentialByID(ctx, m.db, rawID)
	if err != nil {
		return nil, fmt.Errorf("load auth credential: %w", err)
	}

	return m.loadUserByID(ctx, credential.UserID)
}

func (m *Manager) createSession(ctx context.Context, userID int64, now time.Time) (SessionIssue, error) {
	sessionID, err := randomToken(sessionIDTokenBytes)
	if err != nil {
		return SessionIssue{}, fmt.Errorf("generate session id: %w", err)
	}

	token, err := randomToken(sessionTokenBytes)
	if err != nil {
		return SessionIssue{}, fmt.Errorf("generate session token: %w", err)
	}

	csrfToken, err := randomToken(csrfTokenBytes)
	if err != nil {
		return SessionIssue{}, fmt.Errorf("generate csrf token: %w", err)
	}

	record := store.AuthSessionRecord{
		CreatedAt:        now,
		ExpiresAt:        now.Add(m.sessionTTL),
		LastSeenAt:       now,
		RevokedAt:        sql.NullTime{Time: time.Time{}, Valid: false},
		SessionID:        sessionID,
		CSRFToken:        csrfToken,
		SessionTokenHash: sha256Bytes([]byte(token)),
		UserID:           userID,
	}

	err = store.CreateAuthSession(ctx, m.db, &record)
	if err != nil {
		return SessionIssue{}, fmt.Errorf("create auth session: %w", err)
	}

	return SessionIssue{
		SessionID:   sessionID,
		CookieValue: formatSessionCookie(sessionID, token),
		CSRFToken:   csrfToken,
		UserID:      userID,
	}, nil
}

func (m *Manager) storeChallenge(
	ctx context.Context,
	flow string,
	userID sql.NullInt64,
	session *webauthn.SessionData,
) (string, error) {
	blob, err := json.Marshal(session)
	if err != nil {
		return "", fmt.Errorf("marshal webauthn session: %w", err)
	}

	challengeID, err := randomToken(challengeIDBytes)
	if err != nil {
		return "", fmt.Errorf("generate challenge id: %w", err)
	}

	now := time.Now().UTC()

	record := store.AuthChallengeRecord{
		UsedAt:        sql.NullTime{Time: time.Time{}, Valid: false},
		ChallengeID:   challengeID,
		Flow:          flow,
		ChallengeBlob: blob,
		ExpiresAt:     now.Add(m.challengeTTL),
		UserID:        userID,
		CreatedAt:     now,
	}

	err = store.CreateAuthChallenge(ctx, m.db, &record)
	if err != nil {
		return "", fmt.Errorf("store auth challenge: %w", err)
	}

	return challengeID, nil
}

func (m *Manager) consumeChallenge(
	ctx context.Context,
	challengeID, flow string,
) (webauthn.SessionData, store.AuthChallengeRecord, error) {
	record, err := store.ConsumeAuthChallenge(ctx, m.db, challengeID, flow, time.Now().UTC())
	if err != nil {
		if errors.Is(err, store.ErrAuthChallengeMissing) {
			return webauthn.SessionData{}, store.AuthChallengeRecord{}, ErrChallengeNotFound
		}

		return webauthn.SessionData{}, store.AuthChallengeRecord{}, fmt.Errorf("consume auth challenge: %w", err)
	}

	var session webauthn.SessionData

	err = json.Unmarshal(record.ChallengeBlob, &session)
	if err != nil {
		return webauthn.SessionData{}, store.AuthChallengeRecord{}, fmt.Errorf("decode challenge session: %w", err)
	}

	return session, record, nil
}

func (m *Manager) loadUserByID(ctx context.Context, userID int64) (*appUser, error) {
	userRecord, err := store.GetAuthUserByID(ctx, m.db, userID)
	if err != nil {
		return nil, fmt.Errorf("load auth user by id %d: %w", userID, err)
	}

	credentials, err := store.ListAuthCredentialsByUser(ctx, m.db, userID)
	if err != nil {
		return nil, fmt.Errorf("list auth credentials for user %d: %w", userID, err)
	}

	return newAppUser(&userRecord, credentials), nil
}

func (m *Manager) loadUserByHandle(ctx context.Context, handle []byte) (*appUser, error) {
	userRecord, err := store.GetAuthUserByHandle(ctx, m.db, handle)
	if err != nil {
		return nil, fmt.Errorf("load auth user by handle: %w", err)
	}

	credentials, err := store.ListAuthCredentialsByUser(ctx, m.db, userRecord.ID)
	if err != nil {
		return nil, fmt.Errorf("list auth credentials for user %d: %w", userRecord.ID, err)
	}

	return newAppUser(&userRecord, credentials), nil
}

type appUser struct {
	flagsKnown  map[string]bool
	name        string
	displayName string
	handle      []byte
	credentials []webauthn.Credential
	id          int64
}

func newAppUser(user *store.AuthUserRecord, credentials []store.AuthCredentialRecord) *appUser {
	converted := make([]webauthn.Credential, 0, len(credentials))
	flagsKnown := make(map[string]bool, len(credentials))

	for index := range credentials {
		credential := &credentials[index]

		var (
			flags       webauthn.CredentialFlags
			attestation webauthn.CredentialAttestation
		)

		convertedCredential := webauthn.Credential{
			ID:              credential.CredentialID,
			PublicKey:       credential.PublicKey,
			AttestationType: "",
			Transport:       parseTransports(credential.Transports),
			Flags:           flags,
			Attestation:     attestation,
			Authenticator: webauthn.Authenticator{
				AAGUID:       credential.AAGUID,
				SignCount:    credential.SignCount,
				CloneWarning: false,
				Attachment:   "",
			},
		}
		if credential.BackupEligible.Valid {
			convertedCredential.Flags.BackupEligible = credential.BackupEligible.Bool
			flagsKnown[string(credential.CredentialID)] = true
		}

		if credential.BackupState.Valid {
			convertedCredential.Flags.BackupState = credential.BackupState.Bool
		}

		converted = append(converted, convertedCredential)
	}

	return &appUser{
		id:          user.ID,
		handle:      user.UserHandle,
		name:        user.Name,
		displayName: user.DisplayName,
		credentials: converted,
		flagsKnown:  flagsKnown,
	}
}

func (u *appUser) WebAuthnID() []byte {
	return u.handle
}

func (u *appUser) WebAuthnName() string {
	return u.name
}

func (u *appUser) WebAuthnDisplayName() string {
	return u.displayName
}

func (u *appUser) WebAuthnCredentials() []webauthn.Credential {
	return u.credentials
}

func (u *appUser) prepareCredentialForAssertion(rawID []byte, backupEligible, backupState bool) {
	if len(rawID) == 0 {
		return
	}

	key := string(rawID)
	if u.flagsKnown[key] {
		return
	}

	for index := range u.credentials {
		if !bytes.Equal(u.credentials[index].ID, rawID) {
			continue
		}

		u.credentials[index].Flags.BackupEligible = backupEligible
		u.credentials[index].Flags.BackupState = backupState
		u.flagsKnown[key] = true

		return
	}
}

func (u *appUser) credentialDescriptors() []protocol.CredentialDescriptor {
	if len(u.credentials) == 0 {
		return nil
	}

	descriptors := make([]protocol.CredentialDescriptor, 0, len(u.credentials))
	for index := range u.credentials {
		descriptors = append(descriptors, u.credentials[index].Descriptor())
	}

	return descriptors
}

func formatSessionCookie(sessionID, token string) string {
	return sessionID + "." + token
}

//nolint:gocritic // Readability is better with explicit positional returns in this parser helper.
func parseSessionCookie(value string) (string, string, bool) {
	sessionID, token, ok := strings.Cut(value, ".")
	if !ok || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(token) == "" {
		return "", "", false
	}

	return sessionID, token, true
}

func normalizeRecoveryCode(value string) string {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "")
	normalized = strings.ReplaceAll(normalized, " ", "")

	return normalized
}

func joinTransports(values []protocol.AuthenticatorTransport) string {
	if len(values) == 0 {
		return ""
	}

	parts := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(string(value)) == "" {
			continue
		}

		parts = append(parts, string(value))
	}

	return strings.Join(parts, ",")
}

func parseTransports(value string) []protocol.AuthenticatorTransport {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}

	parts := strings.Split(trimmed, ",")

	result := make([]protocol.AuthenticatorTransport, 0, len(parts))
	for _, part := range parts {
		token := strings.TrimSpace(part)
		if token == "" {
			continue
		}

		result = append(result, protocol.AuthenticatorTransport(token))
	}

	return result
}

func randomBytes(size int) ([]byte, error) {
	buf := make([]byte, size)

	_, err := rand.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("read random bytes: %w", err)
	}

	return buf, nil
}

func randomToken(size int) (string, error) {
	raw, err := randomBytes(size)
	if err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func sha256Bytes(raw []byte) []byte {
	hash := sha256.Sum256(raw)

	return hash[:]
}
