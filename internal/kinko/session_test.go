package kinko

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	keyring "github.com/zalando/go-keyring"
)

func TestMain(m *testing.M) {
	prev := sessionSecretStore
	sessionSecretStore = newFakeSecretStore()
	code := m.Run()
	sessionSecretStore = prev
	os.Exit(code)
}

type fakeSecretStore struct {
	data map[string]string
}

func newFakeSecretStore() *fakeSecretStore {
	return &fakeSecretStore{data: map[string]string{}}
}

func (f *fakeSecretStore) key(service, user string) string { return service + "|" + user }

func (f *fakeSecretStore) Get(service, user string) (string, error) {
	v, ok := f.data[f.key(service, user)]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (f *fakeSecretStore) Set(service, user, secret string) error {
	f.data[f.key(service, user)] = secret
	return nil
}

func (f *fakeSecretStore) Delete(service, user string) error {
	k := f.key(service, user)
	if _, ok := f.data[k]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.data, k)
	return nil
}

func withFakeSessionStore(t *testing.T) *fakeSecretStore {
	t.Helper()
	prev := sessionSecretStore
	fake := newFakeSecretStore()
	sessionSecretStore = fake
	t.Cleanup(func() {
		sessionSecretStore = prev
	})
	return fake
}

func TestUnlockSession_TokenDoesNotStoreRawDEK(t *testing.T) {
	withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}

	if err := unlockSession(dataDir, 5*time.Minute, "pw"); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(dataDir, "lock", "session.token"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "dek_b64") {
		t.Fatal("session token must not include raw DEK field")
	}

	var sf sessionFile
	if err := json.Unmarshal(b, &sf); err != nil {
		t.Fatal(err)
	}
	payloadJSON, err := base64.StdEncoding.DecodeString(sf.PayloadB64)
	if err != nil {
		t.Fatal(err)
	}
	var payloadMap map[string]any
	if err := json.Unmarshal(payloadJSON, &payloadMap); err != nil {
		t.Fatal(err)
	}
	if _, ok := payloadMap["dek_b64"]; ok {
		t.Fatal("payload must not include dek_b64")
	}
	if _, ok := payloadMap["enc_dek"]; !ok {
		t.Fatal("payload must include enc_dek")
	}
	var payload sessionPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	var encDEK encryptedBlob
	if err := json.Unmarshal([]byte(payload.EncDEK), &encDEK); err != nil {
		t.Fatal(err)
	}
	if got := encDEK.AADB64; got != base64.StdEncoding.EncodeToString([]byte(aeadContextSessionDEK)) {
		t.Fatalf("session DEK AAD=%q want session context", got)
	}

	dek, err := loadUnlockedDEK(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dek) != dekLength {
		t.Fatalf("unexpected DEK length: %d", len(dek))
	}
}

func TestLoadUnlockedDEK_FailsWhenWrapKeyMismatches(t *testing.T) {
	fake := withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	if err := unlockSession(dataDir, 5*time.Minute, "pw"); err != nil {
		t.Fatal(err)
	}
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	account := sessionWrapKeyAccount(dataDir, meta)
	badKey := base64.StdEncoding.EncodeToString([]byte("00000000000000000000000000000000"))
	if err := fake.Set(sessionWrapKeyService, account, badKey); err != nil {
		t.Fatal(err)
	}

	if _, err := loadUnlockedDEK(dataDir); err == nil {
		t.Fatal("expected locked error when payload cannot be unwrapped")
	}
}

func TestLockSession_RemovesWrapKeyFromStore(t *testing.T) {
	fake := withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	if err := unlockSession(dataDir, 5*time.Minute, "pw"); err != nil {
		t.Fatal(err)
	}
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	account := sessionWrapKeyAccount(dataDir, meta)
	if _, err := fake.Get(sessionWrapKeyService, account); err != nil {
		t.Fatalf("expected wrap key in store before lock: %v", err)
	}
	if err := lockSession(dataDir); err != nil {
		t.Fatal(err)
	}
	if _, err := fake.Get(sessionWrapKeyService, account); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("expected wrap key removal on lock, got: %v", err)
	}
}

func TestDeleteSessionWrapKey_MetaCorruptReturnsError(t *testing.T) {
	withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "vault", "meta.v1.json"), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := deleteSessionWrapKey(dataDir); err == nil {
		t.Fatal("expected error when meta is corrupt")
	}
}

func TestLockSession_SucceedsWhenMetaCorruptIfTokenRemoved(t *testing.T) {
	withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "lock", "session.token"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "vault", "meta.v1.json"), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lockSession(dataDir); err != nil {
		t.Fatalf("lock should succeed even when meta is corrupt: %v", err)
	}
	if fileExists(filepath.Join(dataDir, "lock", "session.token")) {
		t.Fatal("session token should be removed")
	}
}

func TestLockSessionWithWarning_ReportsCleanupFailure(t *testing.T) {
	withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "lock", "session.token"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "vault", "meta.v1.json"), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var warn bytes.Buffer
	if err := lockSessionWithWarning(dataDir, &warn); err != nil {
		t.Fatalf("lock should still succeed: %v", err)
	}
	if !strings.Contains(warn.String(), "warning: session wrap key cleanup failed") {
		t.Fatalf("expected cleanup warning, got: %q", warn.String())
	}
}

func TestUnlockSession_MigratesLegacyPasswordDerivedSessionKey(t *testing.T) {
	withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}

	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	dek, err := unwrapDEKWithPassword(meta, "pw")
	if err != nil {
		t.Fatal(err)
	}
	legacyPub, legacyPriv := deriveSessionKeyPairFromPassword("pw")
	legacyEncPriv, err := encryptBlob(dek, legacyPriv)
	if err != nil {
		t.Fatal(err)
	}
	meta.SessionPubKeyB64 = base64.StdEncoding.EncodeToString(legacyPub)
	meta.EncSessionPrivB64 = legacyEncPriv
	meta.SessionKeySource = ""
	if err := saveMeta(dataDir, meta); err != nil {
		t.Fatal(err)
	}

	if err := unlockSession(dataDir, 5*time.Minute, "pw"); err != nil {
		t.Fatal(err)
	}

	migrated, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.SessionKeySource != sessionKeyRandom {
		t.Fatalf("expected migrated session key source %q, got %q", sessionKeyRandom, migrated.SessionKeySource)
	}
	if migrated.SessionPubKeyB64 == base64.StdEncoding.EncodeToString(legacyPub) {
		t.Fatal("expected unlock to replace legacy password-derived session public key")
	}
	if _, err := loadUnlockedDEK(dataDir); err != nil {
		t.Fatalf("expected migrated session to remain usable: %v", err)
	}
}

// TestUnlockSession_MigrationContendsForMutationLock verifies that when
// unlockSession needs to perform legacy session-key migration, it acquires
// the same mutation lock used by password change before writing metadata.
// acquireMutationLock in this codebase is fail-fast (it does not block
// waiting for a live, non-stale lock; see acquireMetadataLock), so a
// migration-triggering unlock that races a concurrent lock holder must
// surface a lock-conflict error rather than silently proceeding to write
// metadata -- which is exactly the race that could otherwise let a
// migration write revert a just-changed password wrap. Once the lock is
// released, a subsequent migration-triggering unlock must succeed and
// complete the migration normally.
func TestUnlockSession_MigrationContendsForMutationLock(t *testing.T) {
	withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}

	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	dek, err := unwrapDEKWithPassword(meta, "pw")
	if err != nil {
		t.Fatal(err)
	}
	legacyPub, legacyPriv := deriveSessionKeyPairFromPassword("pw")
	legacyEncPriv, err := encryptBlob(dek, legacyPriv)
	if err != nil {
		t.Fatal(err)
	}
	meta.SessionPubKeyB64 = base64.StdEncoding.EncodeToString(legacyPub)
	meta.EncSessionPrivB64 = legacyEncPriv
	meta.SessionKeySource = ""
	if err := saveMeta(dataDir, meta); err != nil {
		t.Fatal(err)
	}

	// Hold the mutation lock externally, simulating a concurrent password
	// change that is mid-flight.
	release, err := acquireMutationLock(dataDir)
	if err != nil {
		t.Fatalf("acquire mutation lock: %v", err)
	}

	if err := unlockSession(dataDir, 5*time.Minute, "pw"); err == nil {
		release()
		t.Fatal("expected migration-triggering unlock to fail to acquire the externally-held mutation lock, but it succeeded")
	} else if !strings.Contains(err.Error(), "mutation lock") && !strings.Contains(err.Error(), "lock exists") {
		release()
		t.Fatalf("expected a lock-contention error, got: %v", err)
	}

	// Metadata must remain untouched (still legacy) since the migration
	// write must never have happened while the lock was held.
	stillLegacy, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if stillLegacy.SessionKeySource != "" {
		t.Fatalf("metadata must not be modified while mutation lock is externally held, got source=%q", stillLegacy.SessionKeySource)
	}

	release()

	// Once the lock is free, the migration-triggering unlock must succeed
	// and actually perform the migration.
	if err := unlockSession(dataDir, 5*time.Minute, "pw"); err != nil {
		t.Fatalf("unlock should succeed once mutation lock is released: %v", err)
	}

	migrated, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.SessionKeySource != sessionKeyRandom {
		t.Fatalf("expected migration to complete after lock release, got source=%q", migrated.SessionKeySource)
	}
}

// TestUnlockSession_NormalUnlockDoesNotBlockOnMutationLock verifies that a
// normal unlock (no legacy migration needed) never acquires the mutation
// lock, so it must return quickly even while the mutation lock is held
// externally for an extended period. This guards against a regression that
// would add lock contention/latency to the common unlock path.
func TestUnlockSession_NormalUnlockDoesNotBlockOnMutationLock(t *testing.T) {
	withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	// A freshly initialized vault already uses the random session key
	// source, so no migration is needed on unlock.
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if meta.SessionKeySource != sessionKeyRandom {
		t.Fatalf("precondition: expected random session key source, got %q", meta.SessionKeySource)
	}

	release, err := acquireMutationLock(dataDir)
	if err != nil {
		t.Fatalf("acquire mutation lock: %v", err)
	}
	t.Cleanup(release)

	done := make(chan error, 1)
	go func() {
		done <- unlockSession(dataDir, 5*time.Minute, "pw")
	}()

	// If unlockSession incorrectly acquired the mutation lock (regression),
	// it would block here until the deferred release() at test end -- so
	// we must always drain `done` (even after a timeout) rather than
	// returning early, to avoid leaking a goroutine that races with
	// TestMain's later restoration of the package-level fake secret store.
	//
	// The timeout is generous (well beyond a single argon2id KDF pass, even
	// under -race / heavy parallel test load) so it only trips on a genuine
	// lock-contention regression, not on machine-load-driven KDF jitter.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("normal unlock should not fail: %v", err)
		}
	case <-time.After(10 * time.Second):
		release()
		<-done
		t.Fatal("normal unlock (no migration needed) blocked while mutation lock was held externally")
	}
}
