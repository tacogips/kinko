package kinko

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func runDoctor(opts globalOptions, stdout io.Writer) error {
	meta, err := loadMeta(opts.dataDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return newCLIError(exitCodeIOFailed, "Vault metadata is missing.", err)
		}
		return newCLIError(exitCodeIOFailed, "Failed to load vault metadata.", err)
	}

	warned := false
	if usesLegacyPasswordDerivedSessionKey(meta) {
		_, _ = fmt.Fprintln(stdout, "WARNING legacy-session-key: session_key_source is missing; unlock once with this release, rotate the vault password, and treat old meta.v1.json backups as sensitive.")
		warned = true
	}
	for _, warning := range diagnoseSessionToken(opts.dataDir, meta) {
		_, _ = fmt.Fprintln(stdout, warning)
		warned = true
	}
	if warned {
		return nil
	}

	_, _ = fmt.Fprintln(stdout, "OK session-key-source: vault metadata uses random session key material.")
	_, _ = fmt.Fprintln(stdout, "OK session-token: no corrupt active session diagnostics.")
	return nil
}

func diagnoseSessionToken(dataDir string, meta *vaultMeta) []string {
	tokenPath := filepath.Join(dataDir, "lock", "session.token")
	b, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil
	}
	var sf sessionFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return []string{"WARNING session-token: session token is not valid JSON."}
	}
	payloadJSON, err := base64.StdEncoding.DecodeString(sf.PayloadB64)
	if err != nil {
		return []string{"WARNING session-token: session token payload is not valid base64."}
	}
	sig, err := base64.StdEncoding.DecodeString(sf.SigB64)
	if err != nil {
		return []string{"WARNING session-token: session token signature is not valid base64."}
	}
	pub, err := sessionPublicKey(meta)
	if err != nil {
		return []string{"WARNING session-token: session public key metadata is invalid."}
	}
	if !ed25519.Verify(pub, payloadJSON, sig) {
		return []string{"WARNING session-token: session token signature does not match current metadata."}
	}
	var payload sessionPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return []string{"WARNING session-token: session token payload is not valid JSON."}
	}
	if time.Now().After(time.Unix(payload.ExpiresAtUnix, 0)) {
		return nil
	}
	wrapKey, err := loadSessionWrapKey(dataDir, meta)
	if err != nil {
		return []string{"WARNING session-token: active session token exists but session wrap key cannot be loaded."}
	}
	dek, err := decryptBlobWithAAD(wrapKey, payload.EncDEK, []byte(aeadContextSessionDEK))
	if err != nil || len(dek) != dekLength {
		return []string{"WARNING session-token: active session token cannot decrypt a valid data key."}
	}
	return nil
}
