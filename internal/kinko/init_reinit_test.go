package kinko

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests cover `kinko init` re-authorizing itself through the exact
// same explosion flow as `kinko explosion` (runExplosionFlow, shared by
// both commands) when the target data dir already holds a fully
// initialized vault, per design-docs/specs/command.md.

func TestRunInit_OverInitializedVault_PipedSuccessRotatesPassword(t *testing.T) {
	withFakeSessionStore(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "old-pw"); err != nil {
		t.Fatal(err)
	}

	opts := globalOptions{dataDir: dataDir, configPath: filepath.Join(t.TempDir(), "bootstrap.toml")}
	token := explosionConfirmationToken(dataDir)
	in := strings.NewReader("old-pw\ny\n" + token + "\nnew-pw\nnew-pw\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runInit(opts, nil, in, &out, &errBuf); err != nil {
		t.Fatalf("runInit over an already-initialized vault failed: %v", err)
	}

	if !strings.Contains(errBuf.String(), "already initialized") {
		t.Fatalf("expected a warning that re-init runs the explosion flow, got stderr: %q", errBuf.String())
	}
	if !strings.Contains(out.String(), "explosion completed: kinko data files removed") {
		t.Fatalf("expected the shared explosion flow's completion message on stdout, got: %q", out.String())
	}
	if !strings.Contains(out.String(), "initialized") {
		t.Fatalf("expected the fresh vault to report initialized, got stdout: %q", out.String())
	}

	if err := unlockSession(dataDir, 5*time.Minute, "old-pw"); err == nil {
		t.Fatal("the old vault's password must not unlock the freshly re-initialized vault")
	}
	if err := unlockSession(dataDir, 5*time.Minute, "new-pw"); err != nil {
		t.Fatalf("the new password must unlock the freshly re-initialized vault: %v", err)
	}
}

func TestRunInit_OverInitializedVault_WrongOldPasswordFailsAndVaultIntact(t *testing.T) {
	withFakeSessionStore(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "old-pw"); err != nil {
		t.Fatal(err)
	}

	opts := globalOptions{dataDir: dataDir, configPath: filepath.Join(t.TempDir(), "bootstrap.toml")}
	token := explosionConfirmationToken(dataDir)
	in := strings.NewReader("wrong-pw\ny\n" + token + "\nnew-pw\nnew-pw\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runInit(opts, nil, in, &out, &errBuf); err == nil {
		t.Fatal("expected runInit to fail when the current vault password is wrong")
	}

	if !isInitializedDataDir(dataDir) {
		t.Fatal("original vault must remain intact after a failed re-authorization")
	}
	if err := unlockSession(dataDir, 5*time.Minute, "old-pw"); err != nil {
		t.Fatalf("original vault must still unlock with its original password: %v", err)
	}
}

func TestRunInit_OverInitializedVault_DeclineConfirmationAbortsWithVaultIntact(t *testing.T) {
	withFakeSessionStore(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "old-pw"); err != nil {
		t.Fatal(err)
	}

	opts := globalOptions{dataDir: dataDir, configPath: filepath.Join(t.TempDir(), "bootstrap.toml")}
	in := strings.NewReader("old-pw\nn\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runInit(opts, nil, in, &out, &errBuf); err != nil {
		t.Fatalf("expected nil error when the user declines the y/N confirmation, got: %v", err)
	}

	if !strings.Contains(out.String(), "aborted") {
		t.Fatalf("expected 'aborted' on stdout, got: %q", out.String())
	}
	if strings.Contains(out.String(), "initialized") {
		t.Fatalf("must not initialize a fresh vault after an abort, stdout: %q", out.String())
	}
	if err := unlockSession(dataDir, 5*time.Minute, "old-pw"); err != nil {
		t.Fatalf("original vault must remain unlockable with its original password: %v", err)
	}
}

func TestRunInit_OverInitializedVault_WrongConfirmationTokenAbortsWithVaultIntact(t *testing.T) {
	withFakeSessionStore(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatal(err)
	}
	if err := initVault(dataDir, "old-pw"); err != nil {
		t.Fatal(err)
	}

	opts := globalOptions{dataDir: dataDir, configPath: filepath.Join(t.TempDir(), "bootstrap.toml")}
	in := strings.NewReader("old-pw\ny\nWRONGTOKEN\n")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	if err := runInit(opts, nil, in, &out, &errBuf); err != nil {
		t.Fatalf("expected nil error when the confirmation token does not match, got: %v", err)
	}

	if !strings.Contains(out.String(), "aborted") {
		t.Fatalf("expected 'aborted' on stdout, got: %q", out.String())
	}
	if err := unlockSession(dataDir, 5*time.Minute, "old-pw"); err != nil {
		t.Fatalf("original vault must remain unlockable with its original password: %v", err)
	}
}

// TestRunInit_PartialVaultArtifact_StillRefusedWithoutExplosionFlow proves
// that a data dir holding only partial/broken vault artifacts (not a
// complete, valid vault) is still refused outright by the pre-existing
// anyVaultArtifact gate, and never enters the explosion authorization flow
// at all: there is no valid vault to verify a password against in that
// case, so the DANGER banner and password prompt must not appear.
func TestRunInit_PartialVaultArtifact_StillRefusedWithoutExplosionFlow(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "vault"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "vault", "config.v1.bin"), []byte("leftover"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := globalOptions{dataDir: dataDir, configPath: filepath.Join(t.TempDir(), "bootstrap.toml")}
	in := strings.NewReader("")
	var out bytes.Buffer
	var errBuf bytes.Buffer
	err := runInit(opts, nil, in, &out, &errBuf)
	if err == nil {
		t.Fatal("expected refusal for partial vault artifacts")
	}
	if !strings.Contains(err.Error(), "partial or complete vault data") {
		t.Fatalf("unexpected error message: %v", err)
	}
	if strings.Contains(errBuf.String(), "DANGER") {
		t.Fatalf("must not enter the explosion flow for partial vault artifacts, got stderr: %q", errBuf.String())
	}
	b, readErr := os.ReadFile(filepath.Join(dataDir, "vault", "config.v1.bin"))
	if readErr != nil {
		t.Fatalf("surviving vault artifact should remain: %v", readErr)
	}
	if string(b) != "leftover" {
		t.Fatalf("surviving vault artifact was modified: %q", string(b))
	}
}
