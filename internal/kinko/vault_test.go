package kinko

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func deriveSessionKeyPairFromPassword(password string) (ed25519.PublicKey, ed25519.PrivateKey) {
	seed := sha256.Sum256([]byte("kinko.session.seed.v1:password:" + strings.TrimSpace(password)))
	priv := ed25519.NewKeyFromSeed(seed[:])
	pub := priv.Public().(ed25519.PublicKey)
	return pub, priv
}

func TestEncryptDecryptBlob(t *testing.T) {
	key := mustRandom(32)
	blob, err := encryptBlob(key, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptBlob(key, blob)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "hello" {
		t.Fatalf("plain=%q", string(plain))
	}
}

func TestEncryptDecryptBlob_WithInjectedMockKeyResolver(t *testing.T) {
	mockResolver := func(_ []byte) ([]byte, error) {
		sum := sha256.Sum256([]byte("kinko-test-mock-key"))
		return sum[:], nil
	}

	blob, err := encryptBlobWithResolver([]byte("short"), []byte("hello"), mockResolver)
	if err != nil {
		t.Fatal(err)
	}

	// Resolver-driven key injection allows deterministic test crypto without runtime flags.
	plain, err := decryptBlobWithResolver([]byte("another-short"), blob, mockResolver)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "hello" {
		t.Fatalf("plain=%q", string(plain))
	}
}

func TestEncryptDecryptBlob_WithAADContext(t *testing.T) {
	key := mustRandom(32)
	blob, err := encryptBlobWithAAD(key, []byte("hello"), []byte(aeadContextVaultData))
	if err != nil {
		t.Fatal(err)
	}

	plain, err := decryptBlobWithAAD(key, blob, []byte(aeadContextVaultData))
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "hello" {
		t.Fatalf("plain=%q", string(plain))
	}

	if _, err := decryptBlobWithAAD(key, blob, []byte(aeadContextConfig)); !errors.Is(err, errDecryptFailed) {
		t.Fatalf("expected context mismatch decrypt failure, got %v", err)
	}
}

func TestDecryptBlobWithAAD_AcceptsLegacyNilAADBlob(t *testing.T) {
	key := mustRandom(32)
	blob, err := encryptBlob(key, []byte("legacy"))
	if err != nil {
		t.Fatal(err)
	}

	plain, err := decryptBlobWithAAD(key, blob, []byte(aeadContextVaultData))
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "legacy" {
		t.Fatalf("plain=%q", string(plain))
	}
}

func TestDeriveSessionKeyPairFromPassword_Deterministic(t *testing.T) {
	password := "test-password"
	pub1, priv1 := deriveSessionKeyPairFromPassword(password)
	pub2, priv2 := deriveSessionKeyPairFromPassword(password)
	if string(pub1) != string(pub2) {
		t.Fatal("expected same public key for same password")
	}
	if string(priv1) != string(priv2) {
		t.Fatal("expected same private key for same password")
	}

	pub3, _ := deriveSessionKeyPairFromPassword(password + "x")
	if string(pub1) == string(pub3) {
		t.Fatal("expected different public key for different passwords")
	}
}

func TestNewVaultUsesRandomSessionKeyMaterial(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "test-password"); err != nil {
		t.Fatal(err)
	}

	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if meta.SessionKeySource != sessionKeyRandom {
		t.Fatalf("unexpected session key source: %q", meta.SessionKeySource)
	}

	legacyPub, _ := deriveSessionKeyPairFromPassword("test-password")
	if meta.SessionPubKeyB64 == base64.StdEncoding.EncodeToString(legacyPub) {
		t.Fatal("new vault must not store password-derived session public key")
	}
}
