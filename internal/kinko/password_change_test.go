package kinko

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupPasswordChangeFixture(t *testing.T, password string) globalOptions {
	t.Helper()
	withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, password); err != nil {
		t.Fatal(err)
	}
	return globalOptions{
		dataDir: dataDir,
		profile: defaultProfile,
		path:    filepath.Clean("/tmp/project"),
	}
}

func TestRunPasswordChange_Success(t *testing.T) {
	opts := setupPasswordChangeFixture(t, "current-password-123")
	if err := unlockSession(opts.dataDir, 5*time.Minute, "current-password-123"); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	in := strings.NewReader("current-password-123\nnext-password-456\n")
	err := runPassword(opts, []string{"change", "--current-stdin", "--new-stdin"}, in, &out, &errBuf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := out.String(); got != "Password changed successfully. Vault is now locked.\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}

	if err := unlockSession(opts.dataDir, 5*time.Minute, "current-password-123"); err == nil {
		t.Fatal("old password should fail after change")
	}
	if err := unlockSession(opts.dataDir, 5*time.Minute, "next-password-456"); err != nil {
		t.Fatalf("new password should unlock: %v", err)
	}
}

func TestRunPasswordChange_TrimsWhitespace(t *testing.T) {
	opts := setupPasswordChangeFixture(t, "current-password-123")

	var out bytes.Buffer
	var errBuf bytes.Buffer
	in := strings.NewReader("  current-password-123  \n  next-password-456  \n")
	err := runPassword(opts, []string{"change", "--current-stdin", "--new-stdin"}, in, &out, &errBuf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := unlockSession(opts.dataDir, 5*time.Minute, "next-password-456"); err != nil {
		t.Fatalf("trimmed new password should unlock: %v", err)
	}
	if err := unlockSession(opts.dataDir, 5*time.Minute, "  next-password-456  "); err == nil {
		t.Fatal("untrimmed new password should not unlock")
	}
}

func TestRunPasswordChange_AllowsShortNewPassword(t *testing.T) {
	opts := setupPasswordChangeFixture(t, "current-password-123")

	var out bytes.Buffer
	var errBuf bytes.Buffer
	in := strings.NewReader("current-password-123\nshort-pass\n")
	err := runPassword(opts, []string{"change", "--current-stdin", "--new-stdin"}, in, &out, &errBuf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := unlockSession(opts.dataDir, 5*time.Minute, "short-pass"); err != nil {
		t.Fatalf("short new password should unlock: %v", err)
	}
}

func TestRunPasswordChange_RejectsSamePasswordWithSpecificMessage(t *testing.T) {
	opts := setupPasswordChangeFixture(t, "current-password-123")

	var out bytes.Buffer
	var errBuf bytes.Buffer
	in := strings.NewReader("current-password-123\ncurrent-password-123\n")
	err := runPassword(opts, []string{"change", "--current-stdin", "--new-stdin"}, in, &out, &errBuf)
	if err == nil {
		t.Fatal("expected same-password rejection")
	}
	if code := ExitCode(err); code != exitCodePolicyFailed {
		t.Fatalf("unexpected exit code: got=%d want=%d err=%v", code, exitCodePolicyFailed, err)
	}
	if got := err.Error(); got != "New password must differ from current password." {
		t.Fatalf("unexpected error message: %q", got)
	}
	if err := unlockSession(opts.dataDir, 5*time.Minute, "current-password-123"); err != nil {
		t.Fatalf("current password should remain valid after rejected change: %v", err)
	}
}

func TestRunPasswordChange_RejectsWhitespaceOnlyPasswordChange(t *testing.T) {
	opts := setupPasswordChangeFixture(t, "current-password-123")

	var out bytes.Buffer
	var errBuf bytes.Buffer
	in := strings.NewReader(" current-password-123 \n  current-password-123  \n")
	err := runPassword(opts, []string{"change", "--current-stdin", "--new-stdin"}, in, &out, &errBuf)
	if err == nil {
		t.Fatal("expected whitespace-only password change rejection")
	}
	if code := ExitCode(err); code != exitCodePolicyFailed {
		t.Fatalf("unexpected exit code: got=%d want=%d err=%v", code, exitCodePolicyFailed, err)
	}
	if got := err.Error(); got != "New password must differ from current password." {
		t.Fatalf("unexpected error message: %q", got)
	}
	if err := unlockSession(opts.dataDir, 5*time.Minute, "current-password-123"); err != nil {
		t.Fatalf("current password should remain valid after rejected whitespace-only change: %v", err)
	}
}

func TestRunPasswordChange_PrioritizesCurrentPasswordAuthOverSamePasswordPolicy(t *testing.T) {
	opts := setupPasswordChangeFixture(t, "current-password-123")

	var out bytes.Buffer
	var errBuf bytes.Buffer
	in := strings.NewReader("wrong-password-123\nwrong-password-123\n")
	err := runPassword(opts, []string{"change", "--current-stdin", "--new-stdin"}, in, &out, &errBuf)
	if err == nil {
		t.Fatal("expected authentication failure")
	}
	if code := ExitCode(err); code != exitCodeAuthFailed {
		t.Fatalf("unexpected exit code: got=%d want=%d err=%v", code, exitCodeAuthFailed, err)
	}
	if got := err.Error(); got != "Current password is invalid." {
		t.Fatalf("unexpected error message: %q", got)
	}
	if err := unlockSession(opts.dataDir, 5*time.Minute, "current-password-123"); err != nil {
		t.Fatalf("current password should remain valid after rejected change: %v", err)
	}
}

func TestNormalizeConfirmedPassword_TrimsBeforeComparison(t *testing.T) {
	got, err := normalizeConfirmedPassword("  next-password-456  ", "next-password-456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "next-password-456" {
		t.Fatalf("unexpected normalized password: got=%q", got)
	}
}

func TestRunPasswordChange_AuthFailureExitCode(t *testing.T) {
	opts := setupPasswordChangeFixture(t, "current-password-123")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	in := strings.NewReader("wrong-password-123\nnext-password-456\n")
	err := runPassword(opts, []string{"change", "--current-stdin", "--new-stdin"}, in, &out, &errBuf)
	if err == nil {
		t.Fatal("expected authentication failure")
	}
	if code := ExitCode(err); code != exitCodeAuthFailed {
		t.Fatalf("unexpected exit code: got=%d want=%d err=%v", code, exitCodeAuthFailed, err)
	}
}

func TestRunPasswordChange_MetadataSafetyValidationExitCode(t *testing.T) {
	opts := setupPasswordChangeFixture(t, "current-password-123")
	meta, err := loadMeta(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	meta.KDFParamsPassword = &kdfParams{
		Algorithm: "argon2id",
		Time:      3,
		Memory:    kdfMaxMemory + 1,
		Threads:   1,
		KeyLen:    dekLength,
	}
	if err := saveMeta(opts.dataDir, meta); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	in := strings.NewReader("current-password-123\nnext-password-456\n")
	err = runPassword(opts, []string{"change", "--current-stdin", "--new-stdin"}, in, &out, &errBuf)
	if err == nil {
		t.Fatal("expected metadata safety validation failure")
	}
	if code := ExitCode(err); code != exitCodeMetadataInvalid {
		t.Fatalf("unexpected exit code: got=%d want=%d err=%v", code, exitCodeMetadataInvalid, err)
	}
}

func TestRunPasswordChange_KeyLenValidationExitCode(t *testing.T) {
	opts := setupPasswordChangeFixture(t, "current-password-123")
	meta, err := loadMeta(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	meta.KDFParamsPassword = &kdfParams{
		Algorithm: "argon2id",
		Time:      3,
		Memory:    kdfMinMemory,
		Threads:   1,
		KeyLen:    31,
	}
	if err := saveMeta(opts.dataDir, meta); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	in := strings.NewReader("current-password-123\nnext-password-456\n")
	err = runPassword(opts, []string{"change", "--current-stdin", "--new-stdin"}, in, &out, &errBuf)
	if err == nil {
		t.Fatal("expected metadata key length validation failure")
	}
	if code := ExitCode(err); code != exitCodeMetadataInvalid {
		t.Fatalf("unexpected exit code: got=%d want=%d err=%v", code, exitCodeMetadataInvalid, err)
	}
}

func TestRunPasswordChange_RevocationFailureDoesNotCommitPasswordChange(t *testing.T) {
	opts := setupPasswordChangeFixture(t, "current-password-123")
	before, err := loadMeta(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(opts.dataDir, "lock", "session.token")
	if err := os.Mkdir(tokenPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tokenPath, "nested"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	in := strings.NewReader("current-password-123\nnext-password-456\n")
	err = runPassword(opts, []string{"change", "--current-stdin", "--new-stdin"}, in, &out, &errBuf)
	if err == nil {
		t.Fatal("expected revocation failure")
	}
	if code := ExitCode(err); code != exitCodeIOFailed {
		t.Fatalf("unexpected exit code: got=%d want=%d err=%v", code, exitCodeIOFailed, err)
	}
	after, err := loadMeta(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if after.WrappedDEKPassB64 != before.WrappedDEKPassB64 || after.SaltPasswordB64 != before.SaltPasswordB64 || after.SessionPubKeyB64 != before.SessionPubKeyB64 || after.EncSessionPrivB64 != before.EncSessionPrivB64 || after.SessionKeySource != before.SessionKeySource {
		t.Fatal("metadata changed despite revocation failure")
	}
}

func TestRunPasswordChange_LegacyVaultUpgradesSessionKeyMetadata(t *testing.T) {
	opts := setupPasswordChangeFixture(t, "current-password-123")
	meta, err := loadMeta(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	oldDEK, err := unwrapDEKWithPassword(meta, "current-password-123")
	if err != nil {
		t.Fatal(err)
	}
	legacyPub, legacyPriv := deriveSessionKeyPairFromPassword("current-password-123")
	legacyEncPriv, err := encryptBlob(oldDEK, legacyPriv)
	if err != nil {
		t.Fatal(err)
	}
	meta.SessionPubKeyB64 = base64.StdEncoding.EncodeToString(legacyPub)
	meta.EncSessionPrivB64 = legacyEncPriv
	meta.SessionKeySource = ""
	if err := saveMeta(opts.dataDir, meta); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	in := strings.NewReader("current-password-123\nnext-password-456\n")
	if err := runPassword(opts, []string{"change", "--current-stdin", "--new-stdin"}, in, &out, &errBuf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	after, err := loadMeta(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if after.SessionKeySource != sessionKeyRandom {
		t.Fatalf("expected session key source %q, got %q", sessionKeyRandom, after.SessionKeySource)
	}
	if after.SessionPubKeyB64 == base64.StdEncoding.EncodeToString(legacyPub) {
		t.Fatal("password change must replace legacy password-derived session public key")
	}
}

func TestRunPasswordChange_MutationLockConflict(t *testing.T) {
	opts := setupPasswordChangeFixture(t, "current-password-123")
	lockPath := filepath.Join(opts.dataDir, "vault", mutationLockFileName)
	if err := os.WriteFile(lockPath, []byte("lock"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	in := strings.NewReader("current-password-123\nnext-password-456\n")
	err := runPassword(opts, []string{"change", "--current-stdin", "--new-stdin"}, in, &out, &errBuf)
	if err == nil {
		t.Fatal("expected lock conflict")
	}
	if code := ExitCode(err); code != exitCodeLockConflict {
		t.Fatalf("unexpected exit code: got=%d want=%d err=%v", code, exitCodeLockConflict, err)
	}
}

func TestReadPasswordFromFD_RejectsOversizedInput(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	payload := strings.Repeat("a", maxPasswordInputBytes+1)
	if _, err := w.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	if _, err := readPasswordFromFD(int(r.Fd())); err == nil {
		t.Fatal("expected oversized password input error")
	}
}

func TestReadPasswordFromFD_TimesOutOnNonEOFStream(t *testing.T) {
	t.Setenv("KINKO_PASSWORD_FD_TIMEOUT", "50ms")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	if _, err := readPasswordFromFD(int(r.Fd())); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestPasswordFDReadTimeout_InvalidEnvFallsBackToDefault(t *testing.T) {
	t.Setenv("KINKO_PASSWORD_FD_TIMEOUT", "invalid")
	if got := passwordFDReadTimeout(); got != defaultPasswordFDReadTimeout {
		t.Fatalf("unexpected timeout fallback: got=%s want=%s", got, defaultPasswordFDReadTimeout)
	}
}
