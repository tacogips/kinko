package kinko

import (
	"crypto/sha256"
	"testing"
)

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
