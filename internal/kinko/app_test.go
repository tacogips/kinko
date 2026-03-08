package kinko

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizePathInput_DirenvPrefix(t *testing.T) {
	in := "-/tmp/project"
	got := normalizePathInput(in)
	if got != "/tmp/project" {
		t.Fatalf("normalizePathInput(%q)=%q", in, got)
	}
}

func TestParseGlobalOptions_PathCleanTrailingSlash(t *testing.T) {
	pathWithSlash := "/tmp/project/"
	opts, rest, err := parseGlobalOptions([]string{
		"--path", pathWithSlash,
		"--kinko-dir", "/tmp/kinko-data",
		"version",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 1 || rest[0] != "version" {
		t.Fatalf("unexpected rest args: %#v", rest)
	}
	want := filepath.Clean("/tmp/project")
	if opts.path != want {
		t.Fatalf("opts.path=%q want=%q", opts.path, want)
	}
}

func TestParseGlobalOptions_PathFromDirenvFormat(t *testing.T) {
	opts, _, err := parseGlobalOptions([]string{
		"--path", "-/tmp/project",
		"--kinko-dir", "/tmp/kinko-data",
		"version",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Clean("/tmp/project")
	if opts.path != want {
		t.Fatalf("opts.path=%q want=%q", opts.path, want)
	}
}

func TestParseGlobalOptions_KinkoDirSet(t *testing.T) {
	opts, _, err := parseGlobalOptions([]string{
		"--kinko-dir", "/tmp/new",
		"version",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Clean("/tmp/new")
	if opts.dataDir != want {
		t.Fatalf("opts.dataDir=%q want=%q", opts.dataDir, want)
	}
}

func TestParseGlobalOptions_ConfigPathSet(t *testing.T) {
	opts, _, err := parseGlobalOptions([]string{
		"--config", "/tmp/custom-bootstrap.toml",
		"version",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Clean("/tmp/custom-bootstrap.toml")
	if opts.configPath != want {
		t.Fatalf("opts.configPath=%q want=%q", opts.configPath, want)
	}
}

func TestParseGlobalOptions_KeychainPreflightSet(t *testing.T) {
	opts, _, err := parseGlobalOptions([]string{
		"--keychain-preflight", "off",
		"version",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.keychainPreflight != "off" {
		t.Fatalf("opts.keychainPreflight=%q want=off", opts.keychainPreflight)
	}
}

func TestParseGlobalOptions_KeychainPreflightInvalid(t *testing.T) {
	_, _, err := parseGlobalOptions([]string{
		"--keychain-preflight", "invalid",
		"version",
	})
	if err == nil {
		t.Fatal("expected invalid keychain-preflight error")
	}
}

func TestIsInitializedDataDir(t *testing.T) {
	d := t.TempDir()
	if isInitializedDataDir(d) {
		t.Fatal("should not be initialized without files")
	}
	if err := os.MkdirAll(filepath.Join(d, "vault"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "vault", "meta.v1.json"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "vault", "vault.v1.bin"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "vault", "config.v1.bin"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !isInitializedDataDir(d) {
		t.Fatal("should be initialized when all vault files exist")
	}
}

func TestRunInit_NonTTYWithoutForceSucceeds(t *testing.T) {
	withFakeSessionStore(t)
	opts := globalOptions{
		dataDir:    filepath.Join(t.TempDir(), "data"),
		configPath: filepath.Join(t.TempDir(), "bootstrap.toml"),
		force:      false,
		confirm:    true,
	}
	in := strings.NewReader("pw\npw\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runInit(opts, nil, in, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "initialized") {
		t.Fatalf("unexpected init output: %q", out.String())
	}
}

func TestRunInit_WithForceSucceeds(t *testing.T) {
	withFakeSessionStore(t)
	opts := globalOptions{
		dataDir:    filepath.Join(t.TempDir(), "data"),
		configPath: filepath.Join(t.TempDir(), "bootstrap.toml"),
		force:      true,
		confirm:    false,
	}
	in := strings.NewReader("pw\npw\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runInit(opts, nil, in, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "initialized") {
		t.Fatalf("unexpected init output: %q", out.String())
	}
}

func TestRunInit_TrimsWhitespaceFromPassword(t *testing.T) {
	withFakeSessionStore(t)
	opts := globalOptions{
		dataDir:    filepath.Join(t.TempDir(), "data"),
		configPath: filepath.Join(t.TempDir(), "bootstrap.toml"),
		force:      false,
		confirm:    true,
	}
	in := strings.NewReader("  pw123456  \n  pw123456  \n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runInit(opts, nil, in, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if err := unlockSession(opts.dataDir, 5*time.Minute, "pw123456"); err != nil {
		t.Fatalf("trimmed password should unlock: %v", err)
	}
	if err := unlockSession(opts.dataDir, 5*time.Minute, "  pw123456  "); err == nil {
		t.Fatal("untrimmed password should not unlock")
	}
}

type failingSecretStore struct{}

func (failingSecretStore) Get(service, user string) (string, error) {
	return "", errors.New("get failed")
}
func (failingSecretStore) Set(service, user, secret string) error { return errors.New("set failed") }
func (failingSecretStore) Delete(service, user string) error      { return nil }

func TestRunInit_KeychainPreflightFailure(t *testing.T) {
	prev := sessionSecretStore
	sessionSecretStore = failingSecretStore{}
	t.Cleanup(func() {
		sessionSecretStore = prev
	})

	opts := globalOptions{
		dataDir:    filepath.Join(t.TempDir(), "data"),
		configPath: filepath.Join(t.TempDir(), "bootstrap.toml"),
		force:      true,
		confirm:    false,
	}
	in := strings.NewReader("pw\npw\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := runInit(opts, nil, in, &out, &errBuf)
	if err == nil {
		t.Fatal("expected keychain preflight failure")
	}
	if !strings.Contains(err.Error(), "keychain preflight failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(opts.dataDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected data dir to not be created on preflight failure, got err=%v", statErr)
	}
}

func TestRunInit_KeychainPreflightBestEffortContinues(t *testing.T) {
	prev := sessionSecretStore
	sessionSecretStore = failingSecretStore{}
	t.Cleanup(func() {
		sessionSecretStore = prev
	})

	opts := globalOptions{
		dataDir:           filepath.Join(t.TempDir(), "data"),
		configPath:        filepath.Join(t.TempDir(), "bootstrap.toml"),
		force:             true,
		confirm:           false,
		keychainPreflight: "best-effort",
	}
	in := strings.NewReader("pw\npw\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runInit(opts, nil, in, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "WARNING: keychain preflight failed") {
		t.Fatalf("expected best-effort warning, got: %q", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "unlock/session may fail later") {
		t.Fatalf("expected late-failure warning, got: %q", errBuf.String())
	}
}

func TestRunInit_KeychainPreflightOffSkipsCheck(t *testing.T) {
	prev := sessionSecretStore
	sessionSecretStore = failingSecretStore{}
	t.Cleanup(func() {
		sessionSecretStore = prev
	})

	opts := globalOptions{
		dataDir:           filepath.Join(t.TempDir(), "data"),
		configPath:        filepath.Join(t.TempDir(), "bootstrap.toml"),
		force:             true,
		confirm:           false,
		keychainPreflight: "off",
	}
	in := strings.NewReader("pw\npw\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runInit(opts, nil, in, &out, &errBuf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBuf.String(), "keychain preflight is disabled") {
		t.Fatalf("expected off-mode warning, got: %q", errBuf.String())
	}
}

func TestRunUnlock_FailsFastWhenKeychainUnavailable(t *testing.T) {
	prev := sessionSecretStore
	sessionSecretStore = failingSecretStore{}
	t.Cleanup(func() {
		sessionSecretStore = prev
	})

	opts := globalOptions{
		dataDir: filepath.Join(t.TempDir(), "data"),
	}
	if err := ensureDirLayout(opts.dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(opts.dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := runUnlock(opts, nil, strings.NewReader("pw\n"), &out, &errBuf)
	if err == nil {
		t.Fatal("expected unlock keychain availability failure")
	}
	if !strings.Contains(err.Error(), "keychain unavailable for unlock") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunUnlock_KeychainPreflightOffSkipsEarlyFailure(t *testing.T) {
	prev := sessionSecretStore
	sessionSecretStore = failingSecretStore{}
	t.Cleanup(func() {
		sessionSecretStore = prev
	})

	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}

	opts := globalOptions{
		dataDir:           dataDir,
		keychainPreflight: "off",
	}
	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := runUnlock(opts, nil, strings.NewReader("pw\n"), &out, &errBuf)
	if err == nil {
		t.Fatal("expected unlock failure")
	}
	if strings.Contains(err.Error(), "keychain unavailable for unlock") {
		t.Fatalf("off mode should skip early preflight failure, got: %v", err)
	}
	if !strings.Contains(err.Error(), "prepare session wrap key") {
		t.Fatalf("expected wrap-key path failure, got: %v", err)
	}
}

func TestRunUnlock_KeychainPreflightBestEffortWarns(t *testing.T) {
	prev := sessionSecretStore
	sessionSecretStore = failingSecretStore{}
	t.Cleanup(func() {
		sessionSecretStore = prev
	})

	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}

	opts := globalOptions{
		dataDir:           dataDir,
		keychainPreflight: "best-effort",
	}
	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := runUnlock(opts, nil, strings.NewReader("pw\n"), &out, &errBuf)
	if err == nil {
		t.Fatal("expected unlock failure")
	}
	if !strings.Contains(errBuf.String(), "WARNING: keychain preflight failed for unlock") {
		t.Fatalf("expected best-effort warning, got: %q", errBuf.String())
	}
}

func TestUnlockWithRetries_CorruptMetaFailsFast(t *testing.T) {
	withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}
	meta, err := loadMeta(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	meta.WrappedDEKPassB64 = "%%%not-base64%%%"
	if err := saveMeta(dataDir, meta); err != nil {
		t.Fatal(err)
	}

	var errBuf bytes.Buffer
	err = unlockWithRetries(globalOptions{dataDir: dataDir}, 5*time.Minute, strings.NewReader("pw\npw\npw\n"), &errBuf, 3)
	if err == nil {
		t.Fatal("expected unlock failure")
	}
	if strings.Contains(err.Error(), "unlock failed after 3 attempts") {
		t.Fatalf("expected fast failure for corruption, got: %v", err)
	}
	if !strings.Contains(err.Error(), "unwrap data key") {
		t.Fatalf("expected unwrap cause, got: %v", err)
	}
}

func TestRunUnlock_AlreadyUnlockedDoesNotPromptForCredential(t *testing.T) {
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

	opts := globalOptions{dataDir: dataDir}
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runUnlock(opts, nil, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatalf("expected unlock to succeed without credential input when already unlocked: %v", err)
	}
	if !strings.Contains(out.String(), "unlocked") {
		t.Fatalf("expected unlocked output, got: %q", out.String())
	}
}

func TestUnlockWithRetries_SurfacesNonCredentialFailure(t *testing.T) {
	withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}

	prev := sessionSecretStore
	sessionSecretStore = failingSecretStore{}
	t.Cleanup(func() {
		sessionSecretStore = prev
	})

	var errBuf bytes.Buffer
	err := unlockWithRetries(globalOptions{dataDir: dataDir}, 5*time.Minute, strings.NewReader("pw\npw\npw\n"), &errBuf, 3)
	if err == nil {
		t.Fatal("expected unlock failure")
	}
	if !strings.Contains(err.Error(), "prepare session wrap key") {
		t.Fatalf("expected keychain error cause, got: %v", err)
	}
}

func TestRunStatus_UnlockedShowsAutoLockTimeInLocalTimezone(t *testing.T) {
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

	var out bytes.Buffer
	if err := runStatus(globalOptions{dataDir: dataDir}, &out); err != nil {
		t.Fatal(err)
	}

	locked, expiresAt, err := sessionStatus(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if locked {
		t.Fatal("expected unlocked session")
	}
	want := "unlocked (auto-lock at " + formatAutoLockTimeLocal(expiresAt) + ")\n"
	if out.String() != want {
		t.Fatalf("unexpected status output: got=%q want=%q", out.String(), want)
	}
}

func TestRunUnlock_ShowsAutoLockTimeWhenAlreadyUnlocked(t *testing.T) {
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

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runUnlock(globalOptions{dataDir: dataDir}, nil, strings.NewReader(""), &out, &errBuf); err != nil {
		t.Fatalf("expected unlock to succeed without credential input when already unlocked: %v", err)
	}

	locked, expiresAt, err := sessionStatus(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if locked {
		t.Fatal("expected unlocked session")
	}
	want := "unlocked (auto-lock at " + formatAutoLockTimeLocal(expiresAt) + ")\n"
	if out.String() != want {
		t.Fatalf("unexpected unlock output: got=%q want=%q", out.String(), want)
	}
}

func TestRunUnlock_ShowsAutoLockTimeAfterSuccessfulUnlock(t *testing.T) {
	withFakeSessionStore(t)
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "pw"); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runUnlock(globalOptions{dataDir: dataDir}, []string{"--timeout", "5m"}, strings.NewReader("pw\n"), &out, &errBuf); err != nil {
		t.Fatal(err)
	}

	locked, expiresAt, err := sessionStatus(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if locked {
		t.Fatal("expected unlocked session")
	}
	want := "unlocked (auto-lock at " + formatAutoLockTimeLocal(expiresAt) + ")\n"
	if out.String() != want {
		t.Fatalf("unexpected unlock output: got=%q want=%q", out.String(), want)
	}
}
