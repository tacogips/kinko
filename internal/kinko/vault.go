package kinko

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	kdfDefaultTime    = 3
	kdfDefaultMemory  = 64 * 1024
	kdfDefaultThreads = 1
	kdfDefaultKeyLen  = 32
	kdfMinTime        = 3
	kdfMinMemory      = 64 * 1024
	kdfMinThreads     = 1
	kdfMaxTime        = 10
	kdfMaxMemory      = 1024 * 1024
	kdfMaxThreads     = 16
	kdfRequiredKeyLen = 32
	saltLength        = 16
	dekLength         = 32
	vaultVersion      = 1
	vaultMarker       = ".kinko-vault-marker"
	sessionKeyRandom  = "random"
)

type vaultMeta struct {
	Version           int        `json:"version"`
	SaltPasswordB64   string     `json:"salt_password_b64"`
	WrappedDEKPassB64 string     `json:"wrapped_dek_pass_b64"`
	SessionPubKeyB64  string     `json:"session_pub_key_b64"`
	EncSessionPrivB64 string     `json:"enc_session_priv_key_b64"`
	SessionKeySource  string     `json:"session_key_source,omitempty"`
	KDFParamsPassword *kdfParams `json:"kdf_params_password,omitempty"`
	UpdatedAt         string     `json:"updated_at,omitempty"`
}

type kdfParams struct {
	Algorithm string `json:"algorithm"`
	Time      uint32 `json:"time"`
	Memory    uint32 `json:"memory"`
	Threads   uint8  `json:"threads"`
	KeyLen    uint32 `json:"key_len"`
}

type vaultData struct {
	Profiles map[string]map[string]map[string]string `json:"profiles"`
	Shared   map[string]string                       `json:"shared,omitempty"`
}

type encryptedBlob struct {
	NonceB64      string `json:"nonce_b64"`
	CiphertextB64 string `json:"ciphertext_b64"`
}

type keyResolver func([]byte) ([]byte, error)

var errDecryptFailed = errors.New("decrypt failed")
var errMetadataInvalid = errors.New("metadata invalid")

func initVault(dataDir string, password string) error {
	saltPass := mustRandom(saltLength)
	dek := mustRandom(dekLength)

	kdf := defaultPasswordKDFParams()
	kekPass := deriveKEK(password, saltPass, kdf)
	wrappedPass, err := encryptBlob(kekPass, dek)
	if err != nil {
		return err
	}
	pubB64, encPriv, err := newRandomSessionKeyMaterial(dek)
	if err != nil {
		return fmt.Errorf("generate session key material: %w", err)
	}

	meta := vaultMeta{
		Version:           vaultVersion,
		SaltPasswordB64:   base64.StdEncoding.EncodeToString(saltPass),
		WrappedDEKPassB64: wrappedPass,
		SessionPubKeyB64:  pubB64,
		EncSessionPrivB64: encPriv,
		SessionKeySource:  sessionKeyRandom,
		KDFParamsPassword: kdf,
		UpdatedAt:         time.Now().UTC().Format(time.RFC3339),
	}
	if err := saveMeta(dataDir, &meta); err != nil {
		return err
	}

	vd := &vaultData{
		Profiles: map[string]map[string]map[string]string{},
		Shared:   map[string]string{},
	}
	if err := saveVault(dataDir, dek, vd); err != nil {
		return err
	}
	if err := saveConfig(dataDir, dek, map[string]string{"unlock_timeout": "9h"}); err != nil {
		return err
	}
	if err := write0600(filepath.Join(dataDir, "vault", vaultMarker), []byte("kinko-vault-v1\n")); err != nil {
		return fmt.Errorf("write vault marker: %w", err)
	}
	return nil
}

func ensureDirLayout(dataDir string) error {
	for _, p := range []string{dataDir, filepath.Join(dataDir, "vault"), filepath.Join(dataDir, "lock")} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			return fmt.Errorf("create dir %s: %w", p, err)
		}
	}
	return nil
}

func saveMeta(dataDir string, meta *vaultMeta) error {
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return write0600(filepath.Join(dataDir, "vault", "meta.v1.json"), b)
}

func loadMeta(dataDir string) (*vaultMeta, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, "vault", "meta.v1.json"))
	if err != nil {
		return nil, err
	}
	var m vaultMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m.Version != vaultVersion {
		return nil, fmt.Errorf("unsupported vault version: %d", m.Version)
	}
	if m.KDFParamsPassword == nil {
		m.KDFParamsPassword = defaultPasswordKDFParams()
	}
	if m.KDFParamsPassword.KeyLen == 0 {
		m.KDFParamsPassword.KeyLen = dekLength
	}
	return &m, nil
}

func saveVault(dataDir string, dek []byte, data *vaultData) error {
	plain, err := json.Marshal(data)
	if err != nil {
		return err
	}
	blob, err := encryptBlob(dek, plain)
	if err != nil {
		return err
	}
	return write0600(filepath.Join(dataDir, "vault", "vault.v1.bin"), []byte(blob))
}

func loadVault(dataDir string, dek []byte) (*vaultData, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, "vault", "vault.v1.bin"))
	if err != nil {
		return nil, err
	}
	plain, err := decryptBlob(dek, string(b))
	if err != nil {
		return nil, err
	}
	var data vaultData
	if err := json.Unmarshal(plain, &data); err != nil {
		return nil, err
	}
	if data.Profiles == nil {
		data.Profiles = map[string]map[string]map[string]string{}
	}
	if data.Shared == nil {
		data.Shared = map[string]string{}
	}
	return &data, nil
}

func saveConfig(dataDir string, dek []byte, cfg map[string]string) error {
	plain, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	blob, err := encryptBlob(dek, plain)
	if err != nil {
		return err
	}
	return write0600(filepath.Join(dataDir, "vault", "config.v1.bin"), []byte(blob))
}

func loadConfig(dataDir string, dek []byte) (map[string]string, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, "vault", "config.v1.bin"))
	if err != nil {
		return nil, err
	}
	plain, err := decryptBlob(dek, string(b))
	if err != nil {
		return nil, err
	}
	var cfg map[string]string
	if err := json.Unmarshal(plain, &cfg); err != nil {
		return nil, err
	}
	if cfg == nil {
		cfg = map[string]string{}
	}
	return cfg, nil
}

func unwrapDEKWithPassword(meta *vaultMeta, password string) ([]byte, error) {
	salt, err := base64.StdEncoding.DecodeString(meta.SaltPasswordB64)
	if err != nil {
		return nil, err
	}
	kdf, err := validatedPasswordKDFParams(meta.KDFParamsPassword)
	if err != nil {
		return nil, errMetadataInvalid
	}
	kek := deriveKEK(password, salt, kdf)
	return decryptBlob(kek, meta.WrappedDEKPassB64)
}

func sessionPrivateKey(meta *vaultMeta, dek []byte) (ed25519.PrivateKey, error) {
	plain, err := decryptBlob(dek, meta.EncSessionPrivB64)
	if err != nil {
		return nil, err
	}
	if len(plain) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid session private key size")
	}
	return ed25519.PrivateKey(plain), nil
}

func sessionPublicKey(meta *vaultMeta) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(meta.SessionPubKeyB64)
	if err != nil {
		return nil, err
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, errors.New("invalid session public key size")
	}
	return ed25519.PublicKey(b), nil
}

func deriveKEK(secret string, salt []byte, params *kdfParams) []byte {
	return argon2.IDKey([]byte(secret), salt, params.Time, params.Memory, params.Threads, params.KeyLen)
}

func deriveSessionKeyPairFromPassword(password string) (ed25519.PublicKey, ed25519.PrivateKey) {
	seed := sha256.Sum256([]byte("kinko.session.seed.v1:password:" + strings.TrimSpace(password)))
	priv := ed25519.NewKeyFromSeed(seed[:])
	pub := priv.Public().(ed25519.PublicKey)
	return pub, priv
}

func generateRandomSessionKeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return pub, priv, nil
}

func newRandomSessionKeyMaterial(dek []byte) (string, string, error) {
	pub, priv, err := generateRandomSessionKeyPair()
	if err != nil {
		return "", "", err
	}
	encPriv, err := encryptBlob(dek, priv)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(pub), encPriv, nil
}

func usesLegacyPasswordDerivedSessionKey(meta *vaultMeta) bool {
	if meta == nil {
		return false
	}
	return strings.TrimSpace(meta.SessionKeySource) == ""
}

func migrateLegacySessionKey(dataDir string, meta *vaultMeta, dek []byte) (*vaultMeta, bool, error) {
	if !usesLegacyPasswordDerivedSessionKey(meta) {
		return meta, false, nil
	}

	next := cloneVaultMeta(meta)
	pubB64, encPriv, err := newRandomSessionKeyMaterial(dek)
	if err != nil {
		return nil, false, err
	}
	next.SessionPubKeyB64 = pubB64
	next.EncSessionPrivB64 = encPriv
	next.SessionKeySource = sessionKeyRandom
	next.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := saveMetaAtomically(dataDir, next); err != nil {
		return nil, false, err
	}
	return next, true, nil
}

func encryptBlob(key, plain []byte) (string, error) {
	return encryptBlobWithResolver(key, plain, resolveAEADKey)
}

func encryptBlobWithResolver(key, plain []byte, resolver keyResolver) (string, error) {
	effectiveKey, err := resolver(key)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(effectiveKey)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := mustRandom(aead.NonceSize())
	ciphertext := aead.Seal(nil, nonce, plain, nil)
	blob := encryptedBlob{
		NonceB64:      base64.StdEncoding.EncodeToString(nonce),
		CiphertextB64: base64.StdEncoding.EncodeToString(ciphertext),
	}
	b, err := json.Marshal(blob)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decryptBlob(key []byte, blobJSON string) ([]byte, error) {
	return decryptBlobWithResolver(key, blobJSON, resolveAEADKey)
}

func decryptBlobWithResolver(key []byte, blobJSON string, resolver keyResolver) ([]byte, error) {
	effectiveKey, err := resolver(key)
	if err != nil {
		return nil, err
	}
	var blob encryptedBlob
	if err := json.Unmarshal([]byte(blobJSON), &blob); err != nil {
		return nil, err
	}
	nonce, err := base64.StdEncoding.DecodeString(blob.NonceB64)
	if err != nil {
		return nil, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(blob.CiphertextB64)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(effectiveKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errDecryptFailed
	}
	return plain, nil
}

func mustRandom(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func defaultPasswordKDFParams() *kdfParams {
	return &kdfParams{
		Algorithm: "argon2id",
		Time:      kdfDefaultTime,
		Memory:    kdfDefaultMemory,
		Threads:   kdfDefaultThreads,
		KeyLen:    kdfDefaultKeyLen,
	}
}

func validatedPasswordKDFParams(raw *kdfParams) (*kdfParams, error) {
	p := defaultPasswordKDFParams()
	if raw != nil {
		*p = *raw
	}
	if p.Algorithm == "" {
		p.Algorithm = "argon2id"
	}
	if p.KeyLen == 0 {
		p.KeyLen = dekLength
	}
	if !strings.EqualFold(p.Algorithm, "argon2id") {
		return nil, fmt.Errorf("%w: unsupported kdf algorithm %q", errMetadataInvalid, p.Algorithm)
	}
	if p.Memory == 0 || p.Time == 0 || p.Threads == 0 {
		return nil, fmt.Errorf("%w: invalid kdf params", errMetadataInvalid)
	}
	if p.Memory > kdfMaxMemory || p.Time > kdfMaxTime || p.Threads > kdfMaxThreads {
		return nil, fmt.Errorf("%w: kdf params exceed safety caps", errMetadataInvalid)
	}
	if p.KeyLen != kdfRequiredKeyLen {
		return nil, fmt.Errorf("%w: unsupported kdf key length", errMetadataInvalid)
	}
	return p, nil
}

func floorEnforcedPasswordKDFParams(raw *kdfParams) (*kdfParams, error) {
	p, err := validatedPasswordKDFParams(raw)
	if err != nil {
		return nil, err
	}
	if p.Memory < kdfMinMemory {
		p.Memory = kdfMinMemory
	}
	if p.Time < kdfMinTime {
		p.Time = kdfMinTime
	}
	if p.Threads < kdfMinThreads {
		p.Threads = kdfMinThreads
	}
	return p, nil
}

func write0600(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return nil
}

func resolveAEADKey(key []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, errors.New("empty encryption key")
	}
	return key, nil
}
