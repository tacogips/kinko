package kinko

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupPermanentUnlockVault(t *testing.T, password string) string {
	t.Helper()
	withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, password); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

func TestPermanentSessionUsesExplicitSignedMarker(t *testing.T) {
	dataDir := setupPermanentUnlockVault(t, "pw")
	if err := unlockSessionWithMode(dataDir, 0, true, "pw"); err != nil {
		t.Fatal(err)
	}

	payload := readSessionPayloadForTest(t, dataDir)
	if !payload.Permanent {
		t.Fatal("permanent session payload must include the explicit permanent marker")
	}
	if payload.ExpiresAtUnix != 0 {
		t.Fatalf("permanent expiry=%d want 0", payload.ExpiresAtUnix)
	}
	locked, state, err := sessionStatusWithState(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if locked || !state.Permanent || !state.ExpiresAt.IsZero() {
		t.Fatalf("unexpected permanent state: locked=%v state=%+v", locked, state)
	}
	if _, err := loadUnlockedDEK(dataDir); err != nil {
		t.Fatalf("permanent session must still load the keychain-wrapped DEK: %v", err)
	}

	if err := lockSession(dataDir); err != nil {
		t.Fatal(err)
	}
	locked, _, err = sessionStatusWithState(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Fatal("manual lock must revoke a permanent session")
	}
}

func TestLegacyZeroExpiryDoesNotBecomePermanent(t *testing.T) {
	dataDir := setupPermanentUnlockVault(t, "pw")
	if err := unlockSession(dataDir, 5*time.Minute, "pw"); err != nil {
		t.Fatal(err)
	}
	dek, err := loadUnlockedDEK(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := sessionPrivateKey(meta, dek)
	if err != nil {
		t.Fatal(err)
	}
	payload := readSessionPayloadForTest(t, dataDir)
	payload.ExpiresAtUnix = 0
	payload.Permanent = false
	writeSignedSessionPayloadForTest(t, dataDir, payload, priv)

	locked, state, err := sessionStatusWithState(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !locked || state.Permanent {
		t.Fatalf("legacy zero-expiry payload must remain locked: locked=%v state=%+v", locked, state)
	}
	warnings := diagnoseSessionToken(dataDir, meta)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "invalid expiry") {
		t.Fatalf("doctor warnings=%v want invalid expiry", warnings)
	}
}

func TestRunUnlockPermanentOutputRefreshAndMutualExclusion(t *testing.T) {
	dataDir := setupPermanentUnlockVault(t, "pw")
	opts := globalOptions{dataDir: dataDir}

	var out bytes.Buffer
	if err := runUnlock(opts, []string{"--permanent"}, strings.NewReader("pw\n"), &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "unlocked (permanent)\n" {
		t.Fatalf("permanent unlock output=%q", got)
	}

	out.Reset()
	if err := runUnlock(opts, nil, strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("plain repeated unlock must not prompt: %v", err)
	}
	if got := out.String(); got != "unlocked (permanent)\n" {
		t.Fatalf("repeated unlock output=%q", got)
	}

	out.Reset()
	if err := runStatus(opts, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "unlocked (permanent)\n" {
		t.Fatalf("status output=%q", got)
	}

	tokenPath := filepath.Join(dataDir, "lock", "session.token")
	before, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	err = runUnlock(opts, []string{"--permanent", "--timeout", "1h"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || ExitCode(err) != exitCodePolicyFailed {
		t.Fatalf("mutual exclusion error=%v code=%d", err, ExitCode(err))
	}
	after, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("mutual-exclusion failure must not mutate the active session")
	}

	err = runUnlock(opts, []string{"--permanent"}, strings.NewReader("wrong\nwrong\nwrong\n"), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || ExitCode(err) != exitCodeAuthFailed {
		t.Fatalf("permanent refresh error=%v code=%d", err, ExitCode(err))
	}
	locked, _, statusErr := sessionStatusWithState(dataDir)
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if !locked {
		t.Fatal("permanent refresh must relock before reauthentication")
	}
}

func TestRunCobraPermanentUnlockRefreshStatusAndLock(t *testing.T) {
	withFakeSessionStore(t)
	dataDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "bootstrap.toml")
	base := []string{"--kinko-dir", dataDir, "--config", configPath}
	if err := Run(append(base, "init"), strings.NewReader("pw123456\npw123456\n"), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := Run(append(base, "unlock", "--timeout", "1m"), strings.NewReader("pw123456\n"), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Run(append(base, "unlock", "--permanent"), strings.NewReader("pw123456\n"), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("Cobra permanent refresh failed: %v", err)
	}
	if got := out.String(); got != "unlocked (permanent)\n" {
		t.Fatalf("Cobra permanent output=%q", got)
	}
	out.Reset()
	if err := Run(append(base, "status"), strings.NewReader(""), &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "unlocked (permanent)\n" {
		t.Fatalf("Cobra status output=%q", got)
	}
	if err := Run(append(base, "lock"), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	locked, _, err := sessionStatusWithState(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Fatal("Cobra lock must revoke permanent session")
	}
}

func TestRunCobraPermanentAndTimeoutFailsBeforePreflight(t *testing.T) {
	err := Run([]string{"--kinko-dir", filepath.Join(t.TempDir(), "missing"), "unlock", "--permanent", "--timeout", "1h"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || ExitCode(err) != exitCodePolicyFailed {
		t.Fatalf("mutual exclusion error=%v code=%d", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPasswordChangeInvalidatesPermanentSession(t *testing.T) {
	opts := setupPasswordChangeFixture(t, "current-password-123")
	if err := unlockSessionWithMode(opts.dataDir, 0, true, "current-password-123"); err != nil {
		t.Fatal(err)
	}
	if err := runPassword(opts, []string{"change", "--current-stdin", "--new-stdin"}, strings.NewReader("current-password-123\nnext-password-456\n"), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	locked, _, err := sessionStatusWithState(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Fatal("password change must invalidate permanent session")
	}
}

func TestDoctorTreatsPermanentSessionAsActive(t *testing.T) {
	fake := withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	if err := unlockSessionWithMode(dataDir, 0, true, "pw"); err != nil {
		t.Fatal(err)
	}
	for key := range fake.data {
		delete(fake.data, key)
	}

	var out bytes.Buffer
	if err := runDoctor(globalOptions{dataDir: dataDir}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "active session token exists but session wrap key cannot be loaded") {
		t.Fatalf("doctor did not diagnose active permanent session: %q", out.String())
	}
}

func readSessionPayloadForTest(t *testing.T, dataDir string) sessionPayload {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dataDir, "lock", "session.token"))
	if err != nil {
		t.Fatal(err)
	}
	var sf sessionFile
	if err := json.Unmarshal(b, &sf); err != nil {
		t.Fatal(err)
	}
	payloadJSON, err := base64.StdEncoding.DecodeString(sf.PayloadB64)
	if err != nil {
		t.Fatal(err)
	}
	var payload sessionPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func writeSignedSessionPayloadForTest(t *testing.T, dataDir string, payload sessionPayload, priv ed25519.PrivateKey) {
	t.Helper()
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	sf := sessionFile{
		PayloadB64: base64.StdEncoding.EncodeToString(payloadJSON),
		SigB64:     base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payloadJSON)),
	}
	b, err := json.Marshal(sf)
	if err != nil {
		t.Fatal(err)
	}
	if err := write0600Atomically(filepath.Join(dataDir, "lock", "session.token"), b); err != nil {
		t.Fatal(err)
	}
}
