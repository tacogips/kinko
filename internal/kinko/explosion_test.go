package kinko

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	keyring "github.com/zalando/go-keyring"
)

type deleteFailSecretStore struct {
	*fakeSecretStore
}

func (s deleteFailSecretStore) Delete(service, user string) error {
	return errors.New("forced delete failure")
}

func TestRunExplosion_DoubleConfirmAndWipe(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}

	opts := globalOptions{dataDir: dataDir, configPath: filepath.Join(t.TempDir(), "bootstrap.toml")}
	token := explosionConfirmationToken(dataDir)
	in := bytes.NewBufferString("pw\ny\n" + token + "\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runExplosion(opts, in, &out, &errBuf); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(dataDir); err != nil {
		t.Fatalf("expected data dir to remain: %v", err)
	}
	if fileExists(filepath.Join(dataDir, "vault", "meta.v1.json")) {
		t.Fatal("expected meta to be removed")
	}
	if fileExists(filepath.Join(dataDir, "vault", "vault.v1.bin")) {
		t.Fatal("expected vault blob to be removed")
	}
	if fileExists(filepath.Join(dataDir, "vault", "config.v1.bin")) {
		t.Fatal("expected config blob to be removed")
	}
	if fileExists(filepath.Join(dataDir, "vault", vaultMarker)) {
		t.Fatal("expected vault marker to be removed")
	}
}

func TestRunExplosion_Abort(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}

	opts := globalOptions{dataDir: dataDir, configPath: filepath.Join(t.TempDir(), "bootstrap.toml")}
	in := bytes.NewBufferString("pw\nn\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runExplosion(opts, in, &out, &errBuf); err != nil {
		t.Fatal(err)
	}

	if !fileExists(filepath.Join(dataDir, "vault", "meta.v1.json")) {
		t.Fatal("expected data to remain after abort")
	}
}

func TestRunExplosion_WrongPassword(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}

	opts := globalOptions{dataDir: dataDir, configPath: filepath.Join(t.TempDir(), "bootstrap.toml")}
	in := bytes.NewBufferString("wrong\ny\nBADTOKEN\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runExplosion(opts, in, &out, &errBuf); err == nil {
		t.Fatal("expected password verification failure")
	}
	if !fileExists(filepath.Join(dataDir, "vault", "meta.v1.json")) {
		t.Fatal("data should remain on password verification failure")
	}
}

func TestRunExplosion_BadTokenAborts(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}

	opts := globalOptions{dataDir: dataDir, configPath: filepath.Join(t.TempDir(), "bootstrap.toml")}
	in := bytes.NewBufferString("pw\ny\nWRONGTOKEN\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runExplosion(opts, in, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(dataDir, "vault", "meta.v1.json")) {
		t.Fatal("data should remain on token mismatch")
	}
}

func TestRunExplosion_MissingMarkerRejected(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dataDir, "vault", vaultMarker)); err != nil {
		t.Fatal(err)
	}

	opts := globalOptions{dataDir: dataDir, configPath: filepath.Join(t.TempDir(), "bootstrap.toml")}
	token := explosionConfirmationToken(dataDir)
	in := bytes.NewBufferString("pw\ny\n" + token + "\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runExplosion(opts, in, &out, &errBuf); err == nil {
		t.Fatal("expected marker check failure")
	}
}

func TestRunExplosion_UnexpectedRootFileRejected(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "note.txt"), []byte("unexpected"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := globalOptions{dataDir: dataDir, configPath: filepath.Join(t.TempDir(), "bootstrap.toml")}
	token := explosionConfirmationToken(dataDir)
	in := bytes.NewBufferString("pw\ny\n" + token + "\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runExplosion(opts, in, &out, &errBuf); err == nil {
		t.Fatal("expected unexpected root file validation failure")
	}
	if !fileExists(filepath.Join(dataDir, "vault", "meta.v1.json")) {
		t.Fatal("data should remain when validation fails")
	}
}

func TestRunExplosion_UnexpectedVaultFileRejected(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "vault", "extra.txt"), []byte("unexpected"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := globalOptions{dataDir: dataDir, configPath: filepath.Join(t.TempDir(), "bootstrap.toml")}
	token := explosionConfirmationToken(dataDir)
	in := bytes.NewBufferString("pw\ny\n" + token + "\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runExplosion(opts, in, &out, &errBuf); err == nil {
		t.Fatal("expected unexpected vault file validation failure")
	}
	if !fileExists(filepath.Join(dataDir, "vault", "meta.v1.json")) {
		t.Fatal("data should remain when validation fails")
	}
}

func TestRunExplosion_RemovesWrapKeyFromStore(t *testing.T) {
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
		t.Fatalf("expected wrap key to exist before explosion: %v", err)
	}

	opts := globalOptions{dataDir: dataDir, configPath: filepath.Join(t.TempDir(), "bootstrap.toml")}
	token := explosionConfirmationToken(dataDir)
	in := bytes.NewBufferString("pw\ny\n" + token + "\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runExplosion(opts, in, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if _, err := fake.Get(sessionWrapKeyService, account); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("expected wrap key removal after explosion, got: %v", err)
	}
}

func TestRunExplosion_ContinuesWhenWrapKeyCleanupFails(t *testing.T) {
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

	prev := sessionSecretStore
	sessionSecretStore = deleteFailSecretStore{fake}
	t.Cleanup(func() {
		sessionSecretStore = prev
	})

	opts := globalOptions{dataDir: dataDir, configPath: filepath.Join(t.TempDir(), "bootstrap.toml")}
	token := explosionConfirmationToken(dataDir)
	in := bytes.NewBufferString("pw\ny\n" + token + "\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runExplosion(opts, in, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "warning: session wrap key cleanup failed") {
		t.Fatalf("expected cleanup warning, got: %q", errBuf.String())
	}
	if fileExists(filepath.Join(dataDir, "vault", "meta.v1.json")) {
		t.Fatal("expected meta to be removed even when wrap key cleanup fails")
	}
}
