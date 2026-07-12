package kinko

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupRestoreSourceArchive builds a source vault via setupBackupFixture
// (A=one, B=two local; SHARED=shared shared scope), backs it up with
// runBackup, and returns the source globalOptions plus the produced archive
// path. Mirrors backup_test.go's own archive-producing pattern.
func setupRestoreSourceArchive(t *testing.T) (globalOptions, string) {
	t.Helper()
	srcOpts := setupBackupFixture(t)
	destDir := t.TempDir()
	var out bytes.Buffer
	if err := runBackup(srcOpts, []string{"--current-stdin", "--dest-path", destDir}, strings.NewReader("pw\n"), &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("backup failed: %v", err)
	}
	archivePath := strings.TrimSpace(strings.TrimPrefix(out.String(), "backup written: "))
	if archivePath == "" {
		t.Fatalf("missing backup output path: %q", out.String())
	}
	return srcOpts, archivePath
}

// freshRestoreTargetOptions builds a globalOptions pointing at a brand-new,
// never-initialized data dir and config path, suitable as a runRestoreWithOptions
// target.
func freshRestoreTargetOptions(t *testing.T) globalOptions {
	t.Helper()
	return globalOptions{
		dataDir:    t.TempDir(),
		configPath: filepath.Join(t.TempDir(), "bootstrap.toml"),
		profile:    defaultProfile,
		path:       filepath.Clean("/tmp/project"),
	}
}

// assertNoVaultArtifacts fails the test if any vault artifact exists under
// dataDir, used to confirm a failed restore left the target untouched.
func assertNoVaultArtifacts(t *testing.T, dataDir string) {
	t.Helper()
	if anyVaultArtifact(dataDir) {
		t.Fatalf("expected no vault artifacts under %s after failed restore", dataDir)
	}
}

// TestRestoreE2E_RoundTrip backs up a source vault (local A=one, B=two;
// shared SHARED=shared) and restores it into a fresh target dir, then
// confirms the same password unlocks the restored vault and every secret
// reads back correctly.
func TestRestoreE2E_RoundTrip(t *testing.T) {
	srcOpts, archivePath := setupRestoreSourceArchive(t)
	targetOpts := freshRestoreTargetOptions(t)

	var out bytes.Buffer
	err := runRestoreWithOptions(targetOpts, restoreOptions{
		input:       restoreInputOptions{currentStdin: true, currentFD: -1},
		archivePath: archivePath,
	}, strings.NewReader("pw\n"), &out, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if !strings.Contains(out.String(), "restore complete") {
		t.Fatalf("unexpected restore output: %q", out.String())
	}
	if !strings.Contains(out.String(), "LOCKED") {
		t.Fatalf("expected restored-locked note in output: %q", out.String())
	}

	if err := unlockSession(targetOpts.dataDir, 5*time.Minute, "pw"); err != nil {
		t.Fatalf("restored vault should unlock with source password: %v", err)
	}

	// Compare restored secrets against the source vault, using the same
	// profile/path scope the fixture used.
	targetOpts.profile = srcOpts.profile
	targetOpts.path = srcOpts.path
	if got := valueAtScope(t, targetOpts, "A"); got != "one" {
		t.Fatalf("restored A=%q want=%q", got, "one")
	}
	if got := valueAtScope(t, targetOpts, "B"); got != "two" {
		t.Fatalf("restored B=%q want=%q", got, "two")
	}
	if got := valueAtShared(t, targetOpts, "SHARED"); got != "shared" {
		t.Fatalf("restored SHARED=%q want=%q", got, "shared")
	}
}

// TestRestoreE2E_WrongPassword restores a valid archive with the wrong
// password: expect failure and no vault artifacts written.
//
// Exit code note: per design-docs/specs/design-restore.md's Password
// Semantics section, ZipCrypto's per-entry check byte is only 1 byte, so a
// wrong password is "primarily detected by CRC mismatch after decryption and
// definitively by the DEK unwrap verification step" - readPasswordLockedZip
// (internal/kinko/restore_zip.go) only classifies a wrong password as
// kind=Auth when *every* entry's check byte fails; with multiple archive
// entries, a wrong password has a small but real per-run chance that one
// (but not all) check bytes accidentally match, which classifies as
// kind=Policy ("archive integrity check failed") instead. This is expected,
// documented, statistical behavior of the weak ZipCrypto layer, not a
// restore.go defect, so this test accepts either exitCodeAuthFailed or
// exitCodePolicyFailed, mirroring TestRestoreE2E_TamperedArchive's same
// acceptance below. In both outcomes, no vault artifacts are ever written to
// the target, which is the safety property that actually matters.
func TestRestoreE2E_WrongPassword(t *testing.T) {
	_, archivePath := setupRestoreSourceArchive(t)
	targetOpts := freshRestoreTargetOptions(t)

	err := runRestoreWithOptions(targetOpts, restoreOptions{
		input:       restoreInputOptions{currentStdin: true, currentFD: -1},
		archivePath: archivePath,
	}, strings.NewReader("wrong\n"), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected restore to fail with wrong password")
	}
	code := ExitCode(err)
	if code != exitCodeAuthFailed && code != exitCodePolicyFailed {
		t.Fatalf("ExitCode(err)=%d want one of {%d,%d} (%v)", code, exitCodeAuthFailed, exitCodePolicyFailed, err)
	}
	assertNoVaultArtifacts(t, targetOpts.dataDir)
}

// TestRestoreE2E_ExistingVaultAtTarget pre-initializes a vault at the target
// with a different password, then attempts restore: expect
// exitCodePolicyFailed and the pre-existing vault left byte-for-byte
// untouched.
func TestRestoreE2E_ExistingVaultAtTarget(t *testing.T) {
	_, archivePath := setupRestoreSourceArchive(t)
	targetOpts := freshRestoreTargetOptions(t)

	if err := ensureDirLayout(targetOpts.dataDir); err != nil {
		t.Fatalf("ensure dir layout: %v", err)
	}
	if err := initVault(targetOpts.dataDir, "other-pw"); err != nil {
		t.Fatalf("init target vault: %v", err)
	}
	metaPath := filepath.Join(targetOpts.dataDir, "vault", "meta.v1.json")
	before, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read pre-existing meta: %v", err)
	}

	err = runRestoreWithOptions(targetOpts, restoreOptions{
		input:       restoreInputOptions{currentStdin: true, currentFD: -1},
		archivePath: archivePath,
	}, strings.NewReader("pw\n"), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected restore to fail when target already has a vault")
	}
	if code := ExitCode(err); code != exitCodePolicyFailed {
		t.Fatalf("ExitCode(err)=%d want=%d (%v)", code, exitCodePolicyFailed, err)
	}

	after, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read meta after rejected restore: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("pre-existing target vault meta.v1.json was modified by rejected restore")
	}
	if err := unlockSession(targetOpts.dataDir, 5*time.Minute, "other-pw"); err != nil {
		t.Fatalf("pre-existing target vault should still unlock with its own password: %v", err)
	}
}

// TestRestoreE2E_TamperedArchive flips a byte inside a vault entry's
// ciphertext (mirroring restore_zip_test.go's tampering technique) and
// confirms restore fails without writing anything to the target.
func TestRestoreE2E_TamperedArchive(t *testing.T) {
	_, archivePath := setupRestoreSourceArchive(t)

	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	patched := append([]byte(nil), data...)
	// First local file header starts at offset 0; flip a byte well inside
	// the first entry's ciphertext payload, past the 12-byte ZipCrypto
	// header, mirroring TestReadPasswordLockedZip_TamperedCiphertextByteIsPolicyNotAuth.
	nameLen := binary.LittleEndian.Uint16(patched[26:28])
	payloadStart := 30 + int(nameLen)
	tamperOffset := payloadStart + zipCryptoHeaderSize + 1
	if tamperOffset >= len(patched) {
		t.Fatalf("archive too small to tamper at offset %d (len=%d)", tamperOffset, len(patched))
	}
	patched[tamperOffset] ^= 0xFF
	if err := os.WriteFile(archivePath, patched, 0o600); err != nil {
		t.Fatal(err)
	}

	targetOpts := freshRestoreTargetOptions(t)
	err = runRestoreWithOptions(targetOpts, restoreOptions{
		input:       restoreInputOptions{currentStdin: true, currentFD: -1},
		archivePath: archivePath,
	}, strings.NewReader("pw\n"), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected restore to fail against tampered archive")
	}
	code := ExitCode(err)
	if code != exitCodeAuthFailed && code != exitCodePolicyFailed {
		t.Fatalf("ExitCode(err)=%d want one of {%d,%d} (%v)", code, exitCodeAuthFailed, exitCodePolicyFailed, err)
	}
	assertNoVaultArtifacts(t, targetOpts.dataDir)
}

// TestRestoreE2E_HostileArchives exercises a handful of hostile/malformed
// archive shapes through the full runRestoreWithOptions path, reusing the
// same fixture builders as restore_manifest_test.go, and confirms each maps
// to exitCodePolicyFailed with the target left untouched.
func TestRestoreE2E_HostileArchives(t *testing.T) {
	buildArchivePath := func(t *testing.T, entries []backupArchiveEntry, password string) string {
		t.Helper()
		dir := t.TempDir()
		archivePath := filepath.Join(dir, "hostile.zip")
		if err := writePasswordLockedZip(archivePath, password, entries); err != nil {
			t.Fatalf("writePasswordLockedZip: %v", err)
		}
		return archivePath
	}

	t.Run("unexpected entry name", func(t *testing.T) {
		entries := buildManifestFixtureEntries(t, false)
		entries = append(entries, backupArchiveEntry{
			name:    "kinko-backup/unexpected.txt",
			data:    []byte("surprise"),
			modTime: time.Now().UTC(),
		})
		archivePath := buildArchivePath(t, entries, "pw")
		targetOpts := freshRestoreTargetOptions(t)

		err := runRestoreWithOptions(targetOpts, restoreOptions{
			input:       restoreInputOptions{currentStdin: true, currentFD: -1},
			archivePath: archivePath,
		}, strings.NewReader("pw\n"), &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("expected restore to reject unexpected entry name")
		}
		if code := ExitCode(err); code != exitCodePolicyFailed {
			t.Fatalf("ExitCode(err)=%d want=%d (%v)", code, exitCodePolicyFailed, err)
		}
		assertNoVaultArtifacts(t, targetOpts.dataDir)
	})

	t.Run("missing required entry", func(t *testing.T) {
		entries := buildManifestFixtureEntries(t, false)
		filtered := make([]backupArchiveEntry, 0, len(entries))
		for _, e := range entries {
			if e.name == "kinko-backup/vault/config.v1.bin" {
				continue
			}
			filtered = append(filtered, e)
		}
		archivePath := buildArchivePath(t, filtered, "pw")
		targetOpts := freshRestoreTargetOptions(t)

		err := runRestoreWithOptions(targetOpts, restoreOptions{
			input:       restoreInputOptions{currentStdin: true, currentFD: -1},
			archivePath: archivePath,
		}, strings.NewReader("pw\n"), &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("expected restore to reject missing required entry")
		}
		if code := ExitCode(err); code != exitCodePolicyFailed {
			t.Fatalf("ExitCode(err)=%d want=%d (%v)", code, exitCodePolicyFailed, err)
		}
		assertNoVaultArtifacts(t, targetOpts.dataDir)
	})

	t.Run("wrong compression method", func(t *testing.T) {
		entries := buildManifestFixtureEntries(t, false)
		archivePath := buildArchivePath(t, entries, "pw")

		data, err := os.ReadFile(archivePath)
		if err != nil {
			t.Fatal(err)
		}
		patched := append([]byte(nil), data...)
		// Local header compression method at offset 8 of the first local
		// file header (offset 0), mirroring
		// TestReadPasswordLockedZip_WrongCompressionMethodIsPolicyError.
		binary.LittleEndian.PutUint16(patched[8:10], 8) // DEFLATE, unsupported.
		eocdStart := len(patched) - 22
		cdOffset := binary.LittleEndian.Uint32(patched[eocdStart+16 : eocdStart+20])
		binary.LittleEndian.PutUint16(patched[int(cdOffset)+10:int(cdOffset)+12], 8)
		if err := os.WriteFile(archivePath, patched, 0o600); err != nil {
			t.Fatal(err)
		}

		targetOpts := freshRestoreTargetOptions(t)
		err = runRestoreWithOptions(targetOpts, restoreOptions{
			input:       restoreInputOptions{currentStdin: true, currentFD: -1},
			archivePath: archivePath,
		}, strings.NewReader("pw\n"), &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("expected restore to reject wrong compression method")
		}
		if code := ExitCode(err); code != exitCodePolicyFailed {
			t.Fatalf("ExitCode(err)=%d want=%d (%v)", code, exitCodePolicyFailed, err)
		}
		assertNoVaultArtifacts(t, targetOpts.dataDir)
	})
}

// TestRestoreE2E_IncludeBootstrap covers --include-bootstrap on (bootstrap
// config restored, content matches original) and off (default; no file
// written at the target config path).
func TestRestoreE2E_IncludeBootstrap(t *testing.T) {
	t.Run("include-bootstrap on writes matching bootstrap config", func(t *testing.T) {
		srcOpts, archivePath := setupRestoreSourceArchive(t)
		originalBootstrap, err := os.ReadFile(srcOpts.configPath)
		if err != nil {
			t.Fatalf("read source bootstrap config: %v", err)
		}
		targetOpts := freshRestoreTargetOptions(t)

		err = runRestoreWithOptions(targetOpts, restoreOptions{
			input:            restoreInputOptions{currentStdin: true, currentFD: -1},
			archivePath:      archivePath,
			includeBootstrap: true,
		}, strings.NewReader("pw\n"), &bytes.Buffer{}, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("restore with --include-bootstrap failed: %v", err)
		}

		got, err := os.ReadFile(targetOpts.configPath)
		if err != nil {
			t.Fatalf("restored bootstrap config missing: %v", err)
		}
		if !bytes.Equal(got, originalBootstrap) {
			t.Fatalf("restored bootstrap config mismatch: got=%q want=%q", got, originalBootstrap)
		}
	})

	t.Run("include-bootstrap off writes no config file", func(t *testing.T) {
		_, archivePath := setupRestoreSourceArchive(t)
		targetOpts := freshRestoreTargetOptions(t)

		err := runRestoreWithOptions(targetOpts, restoreOptions{
			input:       restoreInputOptions{currentStdin: true, currentFD: -1},
			archivePath: archivePath,
		}, strings.NewReader("pw\n"), &bytes.Buffer{}, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("restore failed: %v", err)
		}

		if _, statErr := os.Stat(targetOpts.configPath); !os.IsNotExist(statErr) {
			t.Fatalf("expected no bootstrap config written without --include-bootstrap, stat err=%v", statErr)
		}
	})
}

// TestRestoreE2E_ExistingBootstrapConfigAtTarget pre-creates a file at the
// target --config path and confirms --include-bootstrap causes
// exitCodePolicyFailed, the vault is not written at all (checkRestoreTargetStatePolicy
// and checkRestoreBootstrapPolicy both run under the mutation lock before
// stageAndCommitRestoreFiles, per runRestoreWithOptions), and the pre-existing
// config file content is unchanged.
func TestRestoreE2E_ExistingBootstrapConfigAtTarget(t *testing.T) {
	_, archivePath := setupRestoreSourceArchive(t)
	targetOpts := freshRestoreTargetOptions(t)

	preExisting := []byte("pre-existing-bootstrap-content\n")
	if err := os.WriteFile(targetOpts.configPath, preExisting, 0o600); err != nil {
		t.Fatalf("pre-create target config: %v", err)
	}

	err := runRestoreWithOptions(targetOpts, restoreOptions{
		input:            restoreInputOptions{currentStdin: true, currentFD: -1},
		archivePath:      archivePath,
		includeBootstrap: true,
	}, strings.NewReader("pw\n"), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected restore to fail when target config path already exists")
	}
	if code := ExitCode(err); code != exitCodePolicyFailed {
		t.Fatalf("ExitCode(err)=%d want=%d (%v)", code, exitCodePolicyFailed, err)
	}

	assertNoVaultArtifacts(t, targetOpts.dataDir)

	got, readErr := os.ReadFile(targetOpts.configPath)
	if readErr != nil {
		t.Fatalf("read target config after rejected restore: %v", readErr)
	}
	if !bytes.Equal(got, preExisting) {
		t.Fatalf("pre-existing target config content changed: got=%q want=%q", got, preExisting)
	}
}

// TestRestoreE2E_MutationLockConflict pre-creates a mutation lock at the
// target (mirroring TestRunBackup_MutationLockConflictExitCode in
// backup_test.go) and confirms restore maps the conflict to
// exitCodeLockConflict. The target's vault/ subdirectory is created first via
// ensureDirLayout since restore's target starts as a completely empty temp
// dir, unlike backup's already-initialized source fixture.
func TestRestoreE2E_MutationLockConflict(t *testing.T) {
	_, archivePath := setupRestoreSourceArchive(t)
	targetOpts := freshRestoreTargetOptions(t)

	if err := ensureDirLayout(targetOpts.dataDir); err != nil {
		t.Fatalf("ensure dir layout: %v", err)
	}
	lockPath := filepath.Join(targetOpts.dataDir, "vault", mutationLockFileName)
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

	err = runRestoreWithOptions(targetOpts, restoreOptions{
		input:       restoreInputOptions{currentStdin: true, currentFD: -1},
		archivePath: archivePath,
	}, strings.NewReader("pw\n"), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected mutation lock conflict")
	}
	if code := ExitCode(err); code != exitCodeLockConflict {
		t.Fatalf("ExitCode(err)=%d want=%d (%v)", code, exitCodeLockConflict, err)
	}
}
