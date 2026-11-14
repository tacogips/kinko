package kinko

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	keyring "github.com/zalando/go-keyring"
)

type sessionPayload struct {
	ExpiresAtUnix int64  `json:"expires_at_unix"`
	EncDEK        string `json:"enc_dek"`
}

type sessionFile struct {
	PayloadB64 string `json:"payload_b64"`
	SigB64     string `json:"sig_b64"`
}

const sessionWrapKeyService = "kinko-session-wrap"

type secretStore interface {
	Get(service, user string) (string, error)
	Set(service, user, secret string) error
	Delete(service, user string) error
}

type osKeyringStore struct{}

func (osKeyringStore) Get(service, user string) (string, error) { return keyring.Get(service, user) }
func (osKeyringStore) Set(service, user, secret string) error {
	return keyring.Set(service, user, secret)
}
func (osKeyringStore) Delete(service, user string) error { return keyring.Delete(service, user) }

var sessionSecretStore secretStore = osKeyringStore{}
var errUnlockCredential = errors.New("unlock failed")

func unlockSession(dataDir string, timeout time.Duration, secret string) error {
	if timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	meta, err := loadMeta(dataDir)
	if err != nil {
		return fmt.Errorf("load meta: %w", err)
	}

	dek, err := unwrapDEKWithPassword(meta, secret)
	if err != nil {
		if isCredentialMismatchError(err) {
			return errUnlockCredential
		}
		return fmt.Errorf("unwrap data key: %w", err)
	}
	meta, _, err = migrateLegacySessionKey(dataDir, meta, dek)
	if err != nil {
		return fmt.Errorf("migrate session key metadata: %w", err)
	}
	wrapKey, err := loadOrCreateSessionWrapKey(dataDir, meta)
	if err != nil {
		return fmt.Errorf("prepare session wrap key: %w", err)
	}
	encDEK, err := encryptBlob(wrapKey, dek)
	if err != nil {
		return fmt.Errorf("encrypt session dek: %w", err)
	}

	priv, err := sessionPrivateKey(meta, dek)
	if err != nil {
		return fmt.Errorf("load session private key: %w", err)
	}

	payload := sessionPayload{
		ExpiresAtUnix: time.Now().Add(timeout).Unix(),
		EncDEK:        encDEK,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	sig := ed25519.Sign(priv, payloadJSON)

	sf := sessionFile{
		PayloadB64: base64.StdEncoding.EncodeToString(payloadJSON),
		SigB64:     base64.StdEncoding.EncodeToString(sig),
	}
	b, err := json.Marshal(sf)
	if err != nil {
		return err
	}
	return write0600(filepath.Join(dataDir, "lock", "session.token"), b)
}

func lockSession(dataDir string) error {
	return lockSessionWithWarning(dataDir, nil)
}

func lockSessionWithWarning(dataDir string, warning io.Writer) error {
	err := os.Remove(filepath.Join(dataDir, "lock", "session.token"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Lock semantics prioritize invalidating the active session token.
	// Keychain cleanup is best-effort to avoid operational dead-ends when metadata is damaged.
	if err := deleteSessionWrapKey(dataDir); err != nil && warning != nil {
		_, _ = fmt.Fprintf(warning, "warning: session wrap key cleanup failed: %v\n", err)
	}
	return nil
}

func sessionStatus(dataDir string) (locked bool, expiresAt time.Time, err error) {
	meta, err := loadMeta(dataDir)
	if err != nil {
		return true, time.Time{}, err
	}
	_, exp, err := verifyAndLoadSessionDEK(dataDir, meta)
	if err != nil {
		return true, time.Time{}, nil
	}
	return false, exp, nil
}

func loadUnlockedDEK(dataDir string) ([]byte, error) {
	meta, err := loadMeta(dataDir)
	if err != nil {
		return nil, err
	}
	dek, _, err := verifyAndLoadSessionDEK(dataDir, meta)
	if err != nil {
		return nil, err
	}
	return dek, nil
}

func verifyAndLoadSessionDEK(dataDir string, meta *vaultMeta) ([]byte, time.Time, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, "lock", "session.token"))
	if err != nil {
		return nil, time.Time{}, errors.New("locked")
	}
	var sf sessionFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return nil, time.Time{}, errors.New("locked")
	}

	payloadJSON, err := base64.StdEncoding.DecodeString(sf.PayloadB64)
	if err != nil {
		return nil, time.Time{}, errors.New("locked")
	}
	sig, err := base64.StdEncoding.DecodeString(sf.SigB64)
	if err != nil {
		return nil, time.Time{}, errors.New("locked")
	}
	pub, err := sessionPublicKey(meta)
	if err != nil {
		return nil, time.Time{}, err
	}
	if !ed25519.Verify(pub, payloadJSON, sig) {
		return nil, time.Time{}, errors.New("locked")
	}

	var payload sessionPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return nil, time.Time{}, errors.New("locked")
	}
	exp := time.Unix(payload.ExpiresAtUnix, 0)
	if time.Now().After(exp) {
		_ = lockSession(dataDir)
		return nil, time.Time{}, errors.New("locked")
	}
	wrapKey, err := loadSessionWrapKey(dataDir, meta)
	if err != nil {
		return nil, time.Time{}, errors.New("locked")
	}
	dek, err := decryptBlob(wrapKey, payload.EncDEK)
	if err != nil {
		return nil, time.Time{}, errors.New("locked")
	}
	if len(dek) != dekLength {
		return nil, time.Time{}, errors.New("locked")
	}
	return dek, exp, nil
}

func loadOrCreateSessionWrapKey(dataDir string, meta *vaultMeta) ([]byte, error) {
	key, err := loadSessionWrapKey(dataDir, meta)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		return nil, err
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	secret := base64.StdEncoding.EncodeToString(key)
	if err := sessionSecretStore.Set(sessionWrapKeyService, sessionWrapKeyAccount(dataDir, meta), secret); err != nil {
		return nil, err
	}
	return key, nil
}

func loadSessionWrapKey(dataDir string, meta *vaultMeta) ([]byte, error) {
	secret, err := sessionSecretStore.Get(sessionWrapKeyService, sessionWrapKeyAccount(dataDir, meta))
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(secret)
	if err != nil {
		return nil, errors.New("invalid session wrap key")
	}
	if len(key) != 32 {
		return nil, errors.New("invalid session wrap key")
	}
	return key, nil
}

func sessionWrapKeyAccount(dataDir string, meta *vaultMeta) string {
	sum := sha256.Sum256([]byte("kinko.session.wrap.account.v1:" + filepath.Clean(dataDir) + ":" + meta.SessionPubKeyB64))
	return "kinko-session-wrap:" + hex.EncodeToString(sum[:])
}

func deleteSessionWrapKey(dataDir string) error {
	meta, err := loadMeta(dataDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("load meta for session wrap key cleanup: %w", err)
	}
	account := sessionWrapKeyAccount(dataDir, meta)
	delErr := sessionSecretStore.Delete(sessionWrapKeyService, account)
	if delErr != nil && !errors.Is(delErr, keyring.ErrNotFound) {
		return fmt.Errorf("remove session wrap key: %w", delErr)
	}
	return nil
}

func ensureSessionSecretStoreReady() error {
	probeAccount := fmt.Sprintf("kinko-session-wrap-probe:%d:%d", os.Getpid(), time.Now().UnixNano())
	probe := make([]byte, 16)
	if _, err := rand.Read(probe); err != nil {
		return fmt.Errorf("generate keychain probe secret: %w", err)
	}
	probeSecret := base64.StdEncoding.EncodeToString(probe)

	if err := sessionSecretStore.Set(sessionWrapKeyService, probeAccount, probeSecret); err != nil {
		return fmt.Errorf("keychain set failed: %w", err)
	}
	defer func() {
		_ = sessionSecretStore.Delete(sessionWrapKeyService, probeAccount)
	}()

	got, err := sessionSecretStore.Get(sessionWrapKeyService, probeAccount)
	if err != nil {
		return fmt.Errorf("keychain get failed: %w", err)
	}
	if got != probeSecret {
		return errors.New("keychain roundtrip mismatch")
	}
	if err := sessionSecretStore.Delete(sessionWrapKeyService, probeAccount); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("keychain delete failed: %w", err)
	}
	return nil
}

func isCredentialMismatchError(err error) bool {
	return errors.Is(err, errDecryptFailed)
}
