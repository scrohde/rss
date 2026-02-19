//nolint:testpackage // Tests need access to unexported helpers in this package.
package auth

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"rss/internal/store"
)

const (
	testRPID     = "example.com"
	testRPOrigin = "https://example.com"
	testRPName   = "Pulse RSS"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "auth.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	t.Cleanup(func() {
		closeErr := db.Close()
		if closeErr != nil {
			t.Errorf("db.Close: %v", closeErr)
		}
	})

	err = store.Init(db)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}

	manager, err := NewManager(db, &Config{
		RPID:         testRPID,
		RPOrigin:     testRPOrigin,
		RPName:       testRPName,
		CookieName:   "",
		SessionTTL:   0,
		ChallengeTTL: 0,
		CookieSecure: false,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	return manager
}

//nolint:gocritic // Returning explicit values keeps test setup compact and clear.
func seedOwnerCredential(t *testing.T, manager *Manager) (store.AuthUserRecord, []byte) {
	t.Helper()

	owner, err := manager.EnsureOwner(context.Background())
	if err != nil {
		t.Fatalf("EnsureOwner: %v", err)
	}

	credentialID := []byte("cred-1")

	err = store.UpsertAuthCredential(context.Background(), manager.db, &store.AuthCredentialRecord{
		CreatedAt:      time.Now().UTC(),
		LastUsedAt:     sql.NullTime{Time: time.Time{}, Valid: false},
		Transports:     "internal",
		CredentialID:   credentialID,
		PublicKey:      []byte("public-key"),
		AAGUID:         []byte("aaguid"),
		ID:             0,
		UserID:         owner.ID,
		SignCount:      1,
		BackupEligible: sql.NullBool{Bool: false, Valid: false},
		BackupState:    sql.NullBool{Bool: false, Valid: false},
	})
	if err != nil {
		t.Fatalf("UpsertAuthCredential: %v", err)
	}

	return owner, credentialID
}

func TestResolveLoginUserByHandle(t *testing.T) {
	t.Parallel()

	manager := newTestManager(t)
	owner, credentialID := seedOwnerCredential(t, manager)

	user, err := manager.resolveLoginUser(context.Background(), credentialID, owner.UserHandle)
	if err != nil {
		t.Fatalf("resolveLoginUser: %v", err)
	}

	if user.id != owner.ID {
		t.Fatalf("expected owner id %d, got %d", owner.ID, user.id)
	}
}

func TestResolveLoginUserFallsBackToCredentialIDWhenHandleMissing(t *testing.T) {
	t.Parallel()

	manager := newTestManager(t)
	owner, credentialID := seedOwnerCredential(t, manager)

	user, err := manager.resolveLoginUser(context.Background(), credentialID, nil)
	if err != nil {
		t.Fatalf("resolveLoginUser: %v", err)
	}

	if user.id != owner.ID {
		t.Fatalf("expected owner id %d, got %d", owner.ID, user.id)
	}
}

func TestResolveLoginUserFallsBackToCredentialIDWhenHandleUnknown(t *testing.T) {
	t.Parallel()

	manager := newTestManager(t)
	owner, credentialID := seedOwnerCredential(t, manager)

	user, err := manager.resolveLoginUser(context.Background(), credentialID, []byte("unknown-handle"))
	if err != nil {
		t.Fatalf("resolveLoginUser: %v", err)
	}

	if user.id != owner.ID {
		t.Fatalf("expected owner id %d, got %d", owner.ID, user.id)
	}
}

func TestResolveLoginUserFailsWithoutHandleOrCredentialID(t *testing.T) {
	t.Parallel()

	manager := newTestManager(t)
	seedOwnerCredential(t, manager)

	_, err := manager.resolveLoginUser(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected resolveLoginUser error")
	}
}
