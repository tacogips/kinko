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
