package kinko

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestRunPasswordChange_RemovesOldSessionWrapKeyAccount(t *testing.T) {
	fake := withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "current-password-123"); err != nil {
		t.Fatal(err)
	}
	opts := globalOptions{
		dataDir: dataDir,
		profile: defaultProfile,
		path:    filepath.Clean("/tmp/project"),
	}
	if err := unlockSession(opts.dataDir, 5*time.Minute, "current-password-123"); err != nil {
		t.Fatal(err)
	}
	before, err := loadMeta(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	oldAccount := sessionWrapKeyAccount(opts.dataDir, before)
	if _, ok := fake.data[fake.key(sessionWrapKeyService, oldAccount)]; !ok {
		t.Fatal("expected old session wrap-key account before password change")
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	in := strings.NewReader("current-password-123\nnext-password-456\n")
	if err := runPassword(opts, []string{"change", "--current-stdin", "--new-stdin"}, in, &out, &errBuf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := fake.data[fake.key(sessionWrapKeyService, oldAccount)]; ok {
		t.Fatal("old session wrap-key account should be removed after password change")
	}
	after, err := loadMeta(opts.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if after.SessionPubKeyB64 == before.SessionPubKeyB64 {
		t.Fatal("password change should rotate session key metadata")
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
	metadata := mutationLockMetadata{
		PID:       os.Getpid(),
		Hostname:  mustHostname(t),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	b, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, b, 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	in := strings.NewReader("current-password-123\nnext-password-456\n")
	err = runPassword(opts, []string{"change", "--current-stdin", "--new-stdin"}, in, &out, &errBuf)
	if err == nil {
		t.Fatal("expected lock conflict")
	}
	if code := ExitCode(err); code != exitCodeLockConflict {
		t.Fatalf("unexpected exit code: got=%d want=%d err=%v", code, exitCodeLockConflict, err)
	}
	if !strings.Contains(errors.Unwrap(err).Error(), lockPath) || !strings.Contains(errors.Unwrap(err).Error(), "remove it manually") {
		t.Fatalf("lock conflict should include recovery guidance, got: %v", err)
	}
}

func TestAcquireMutationLock_WritesMetadataAndReleases(t *testing.T) {
	opts := setupPasswordChangeFixture(t, "current-password-123")
	lockPath := filepath.Join(opts.dataDir, "vault", mutationLockFileName)

	release, err := acquireMutationLock(opts.dataDir)
	if err != nil {
		t.Fatalf("acquire mutation lock: %v", err)
	}
	b, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock metadata: %v", err)
	}
	var metadata mutationLockMetadata
	if err := json.Unmarshal(b, &metadata); err != nil {
		t.Fatalf("decode lock metadata: %v", err)
	}
	if metadata.PID != os.Getpid() || metadata.Hostname == "" || metadata.CreatedAt == "" {
		t.Fatalf("unexpected lock metadata: %#v", metadata)
	}
	release()
	if fileExists(lockPath) {
		t.Fatal("lock file should be removed on release")
	}
}

func TestAcquireMutationLock_TakesOverStaleSameHostLock(t *testing.T) {
	opts := setupPasswordChangeFixture(t, "current-password-123")
	lockPath := filepath.Join(opts.dataDir, "vault", mutationLockFileName)
	metadata := mutationLockMetadata{
		PID:       99999999,
		Hostname:  mustHostname(t),
		CreatedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
	}
	b, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, b, 0o600); err != nil {
		t.Fatal(err)
	}

	release, err := acquireMutationLock(opts.dataDir)
	if err != nil {
		t.Fatalf("stale same-host lock should be taken over: %v", err)
	}
	defer release()
	b, err = os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var current mutationLockMetadata
	if err := json.Unmarshal(b, &current); err != nil {
		t.Fatal(err)
	}
	if current.PID != os.Getpid() {
		t.Fatalf("lock metadata should be replaced by current owner: %#v", current)
	}
}

func TestAcquireMutationLock_CorruptLockBlocks(t *testing.T) {
	opts := setupPasswordChangeFixture(t, "current-password-123")
	lockPath := filepath.Join(opts.dataDir, "vault", mutationLockFileName)
	if err := os.WriteFile(lockPath, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := acquireMutationLock(opts.dataDir)
	if err == nil {
		t.Fatal("expected corrupt lock to block")
	}
	if !strings.Contains(err.Error(), lockPath) || !strings.Contains(err.Error(), "remove it manually") {
		t.Fatalf("unexpected corrupt lock error: %v", err)
	}
}

// TestAcquireMutationLock_ConcurrentStaleTakeoverOnlyOneWinner simulates many
// goroutines racing to take over the same stale lock concurrently. With the
// old remove-then-recreate takeover, two racers could both decide the lock
// is stale, both remove it (one removing the other's freshly-created live
// lock), and both end up believing they hold the lock. The fixed-path
// ".takeover" intent-mutex serializes the actual remove+recreate step, so
// exactly one goroutine may hold the live lock at any instant. Since our
// own successfully-created lock is never "stale" to our own subsequent
// attempts (same PID, running), every other concurrent attempt must observe
// a live conflict rather than also succeeding.
func TestAcquireMutationLock_ConcurrentStaleTakeoverOnlyOneWinner(t *testing.T) {
	opts := setupPasswordChangeFixture(t, "current-password-123")
	lockPath := filepath.Join(opts.dataDir, "vault", mutationLockFileName)
	metadata := mutationLockMetadata{
		PID:       99999999,
		Hostname:  mustHostname(t),
		CreatedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
	}
	b, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, b, 0o600); err != nil {
		t.Fatal(err)
	}

	const attempts = 12
	var wg sync.WaitGroup
	var mu sync.Mutex
	var liveHolders int
	var maxConcurrentHolders int
	errs := make([]error, attempts)
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		i := i
		go func() {
			defer wg.Done()
			release, err := acquireMetadataLock(lockPath)
			errs[i] = err
			if err != nil {
				return
			}
			mu.Lock()
			liveHolders++
			if liveHolders > maxConcurrentHolders {
				maxConcurrentHolders = liveHolders
			}
			mu.Unlock()
			// Hold briefly so any other goroutine that incorrectly
			// believes it also holds the lock would overlap here.
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			liveHolders--
			mu.Unlock()
			release()
		}()
	}
	wg.Wait()

	if maxConcurrentHolders > 1 {
		t.Fatalf("more than one goroutine held the lock simultaneously: max=%d", maxConcurrentHolders)
	}
	successCount := 0
	for i := range attempts {
		if errs[i] == nil {
			successCount++
		}
	}
	if successCount == 0 {
		t.Fatal("expected at least one goroutine to acquire the lock")
	}
	if fileExists(lockPath) {
		t.Fatal("lock file should be removed once all holders released")
	}

	entries, err := os.ReadDir(filepath.Dir(lockPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), metadataLockTakeoverSuffix) {
			t.Fatalf("leftover takeover intent file after resolution: %s", e.Name())
		}
	}
}

func mustHostname(t *testing.T) string {
	t.Helper()
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	return hostname
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
