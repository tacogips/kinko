package kinko

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestRestoreParseOptions covers parseRestoreOptions: positional argument
// requirements and flag parsing/defaults.
func TestRestoreParseOptions(t *testing.T) {
	t.Run("no positional arg is an error", func(t *testing.T) {
		_, err := parseRestoreOptions([]string{})
		if err == nil {
			t.Fatal("expected error for missing archive path argument")
		}
		if got := err.Error(); got != "restore requires exactly one archive path argument" {
			t.Fatalf("unexpected error message: %q", got)
		}
	})

	t.Run("two positional args is an error", func(t *testing.T) {
		_, err := parseRestoreOptions([]string{"a.zip", "b.zip"})
		if err == nil {
			t.Fatal("expected error for multiple archive path arguments")
		}
		if got := err.Error(); got != "restore accepts at most one archive path argument" {
			t.Fatalf("unexpected error message: %q", got)
		}
	})

	t.Run("defaults are correct", func(t *testing.T) {
		opts, err := parseRestoreOptions([]string{"archive.zip"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if opts.archivePath != "archive.zip" {
			t.Fatalf("archivePath=%q want=%q", opts.archivePath, "archive.zip")
		}
		if opts.input.currentFD != -1 {
			t.Fatalf("currentFD default=%d want=-1", opts.input.currentFD)
		}
		if opts.input.currentStdin {
			t.Fatal("currentStdin default should be false")
		}
		if opts.input.forceTTY {
			t.Fatal("forceTTY default should be false")
		}
		if opts.includeBootstrap {
			t.Fatal("includeBootstrap default should be false")
		}
	})

	t.Run("all flags parse correctly", func(t *testing.T) {
		opts, err := parseRestoreOptions([]string{
			"--current-stdin",
			"--current-fd", "3",
			"--force-tty",
			"--include-bootstrap",
			"archive.zip",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !opts.input.currentStdin {
			t.Fatal("currentStdin should be true")
		}
		if opts.input.currentFD != 3 {
			t.Fatalf("currentFD=%d want=3", opts.input.currentFD)
		}
		if !opts.input.forceTTY {
			t.Fatal("forceTTY should be true")
		}
		if !opts.includeBootstrap {
			t.Fatal("includeBootstrap should be true")
		}
		if opts.archivePath != "archive.zip" {
			t.Fatalf("archivePath=%q want=%q", opts.archivePath, "archive.zip")
		}
	})

	t.Run("invalid flag syntax is wrapped", func(t *testing.T) {
		_, err := parseRestoreOptions([]string{"--current-fd", "not-an-int", "archive.zip"})
		if err == nil {
			t.Fatal("expected error for invalid --current-fd value")
		}
	})
}

// TestRestoreCheckTargetStatePolicy covers checkRestoreTargetStatePolicy:
// fresh empty dataDir passes, and each of the four vault artifacts present
// individually causes refusal.
func TestRestoreCheckTargetStatePolicy(t *testing.T) {
	t.Run("fresh empty data dir is allowed", func(t *testing.T) {
		dataDir := t.TempDir()
		if err := checkRestoreTargetStatePolicy(dataDir); err != nil {
			t.Fatalf("unexpected error for fresh data dir: %v", err)
		}
	})

	artifacts := []string{
		"meta.v1.json",
		"vault.v1.bin",
		"config.v1.bin",
		vaultMarker,
	}
	for _, artifact := range artifacts {
		t.Run("existing "+artifact+" is refused", func(t *testing.T) {
			dataDir := t.TempDir()
			vaultDir := filepath.Join(dataDir, "vault")
			if err := os.MkdirAll(vaultDir, 0o700); err != nil {
				t.Fatalf("mkdir vault dir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(vaultDir, artifact), []byte("existing"), 0o600); err != nil {
				t.Fatalf("write artifact: %v", err)
			}
			err := checkRestoreTargetStatePolicy(dataDir)
			if err == nil {
				t.Fatalf("expected refusal when %s already exists", artifact)
			}
		})
	}
}

// TestRestoreCheckBootstrapPolicy covers checkRestoreBootstrapPolicy.
func TestRestoreCheckBootstrapPolicy(t *testing.T) {
	t.Run("includeBootstrap false is always nil", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "bootstrap.toml")
		if err := os.WriteFile(configPath, []byte("existing"), 0o600); err != nil {
			t.Fatalf("write existing config: %v", err)
		}
		if err := checkRestoreBootstrapPolicy(configPath, false); err != nil {
			t.Fatalf("unexpected error when includeBootstrap is false: %v", err)
		}
	})

	t.Run("missing config path is allowed", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "bootstrap.toml")
		if err := checkRestoreBootstrapPolicy(configPath, true); err != nil {
			t.Fatalf("unexpected error for missing config path: %v", err)
		}
	})

	t.Run("existing config path is refused", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "bootstrap.toml")
		if err := os.WriteFile(configPath, []byte("existing"), 0o600); err != nil {
			t.Fatalf("write existing config: %v", err)
		}
		err := checkRestoreBootstrapPolicy(configPath, true)
		if err == nil {
			t.Fatal("expected refusal for existing bootstrap config")
		}
	})
}

// buildFakeValidatedRestoreArchive constructs a validatedRestoreArchive with
// small fake byte slices, suitable for stageAndCommitRestoreFiles tests that
// do not need real vault content.
func buildFakeValidatedRestoreArchive() *validatedRestoreArchive {
	return &validatedRestoreArchive{
		metaJSON:  []byte(`{"fake":"meta"}`),
		vaultBin:  []byte(`{"fake":"vault"}`),
		configBin: []byte(`{"fake":"config"}`),
		marker:    []byte("kinko-vault-v1\n"),
	}
}

// TestRestoreStageAndCommitFiles_Success covers the success path of
// stageAndCommitRestoreFiles.
func TestRestoreStageAndCommitFiles_Success(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatalf("ensure dir layout: %v", err)
	}
	archive := buildFakeValidatedRestoreArchive()

	if err := stageAndCommitRestoreFiles(dataDir, archive); err != nil {
		t.Fatalf("stageAndCommitRestoreFiles failed: %v", err)
	}

	vaultDir := filepath.Join(dataDir, "vault")
	checks := map[string][]byte{
		"meta.v1.json":  archive.metaJSON,
		"vault.v1.bin":  archive.vaultBin,
		"config.v1.bin": archive.configBin,
		vaultMarker:     archive.marker,
	}
	for name, want := range checks {
		got, err := os.ReadFile(filepath.Join(vaultDir, name))
		if err != nil {
			t.Fatalf("read restored file %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Fatalf("restored file %s content mismatch: got=%q want=%q", name, got, want)
		}
	}

	entries, err := os.ReadDir(vaultDir)
	if err != nil {
		t.Fatalf("read vault dir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == restoreVaultTmpSuffix {
			t.Fatalf("leftover restore-tmp file: %s", e.Name())
		}
		if len(e.Name()) >= len(restoreVaultTmpSuffix) && e.Name()[len(e.Name())-len(restoreVaultTmpSuffix):] == restoreVaultTmpSuffix {
			t.Fatalf("leftover restore-tmp file: %s", e.Name())
		}
	}
}

// TestRestoreStageAndCommitFiles_FailureCleansUp induces a deterministic
// failure partway through the commit by pre-creating one of the final
// destination paths (config.v1.bin) as a directory, so the rename into that
// path fails. It asserts that after the function returns an error, none of
// the four final files remain as regular files with restored content -
// specifically the marker must never be left behind (marker-last safety
// property) - and no leftover .restore-tmp files remain either.
func TestRestoreStageAndCommitFiles_FailureCleansUp(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatalf("ensure dir layout: %v", err)
	}
	vaultDir := filepath.Join(dataDir, "vault")

	// Pre-create config.v1.bin as a directory so os.Rename onto it fails.
	if err := os.MkdirAll(filepath.Join(vaultDir, "config.v1.bin"), 0o700); err != nil {
		t.Fatalf("pre-create config.v1.bin directory: %v", err)
	}

	archive := buildFakeValidatedRestoreArchive()
	err := stageAndCommitRestoreFiles(dataDir, archive)
	if err == nil {
		t.Fatal("expected stageAndCommitRestoreFiles to fail")
	}

	// The marker must never be left behind as a regular file.
	if info, statErr := os.Lstat(filepath.Join(vaultDir, vaultMarker)); statErr == nil {
		t.Fatalf("marker should not exist after failed restore, found: %v", info)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unexpected error stat-ing marker: %v", statErr)
	}

	// meta.v1.json and vault.v1.bin (renamed before config.v1.bin in
	// staged-file order) must have been rolled back too.
	for _, name := range []string{"meta.v1.json", "vault.v1.bin"} {
		if info, statErr := os.Lstat(filepath.Join(vaultDir, name)); statErr == nil {
			t.Fatalf("restored file %s should have been rolled back, found: %v", name, info)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("unexpected error stat-ing %s: %v", name, statErr)
		}
	}

	// The pre-created directory at config.v1.bin should remain untouched
	// (cleanup must not have removed the pre-existing directory, since it
	// was never a path *this function* renamed into place).
	info, statErr := os.Lstat(filepath.Join(vaultDir, "config.v1.bin"))
	if statErr != nil {
		t.Fatalf("expected pre-existing config.v1.bin directory to remain: %v", statErr)
	}
	if !info.IsDir() {
		t.Fatal("expected config.v1.bin to remain a directory")
	}

	// No leftover .restore-tmp files.
	entries, err := os.ReadDir(vaultDir)
	if err != nil {
		t.Fatalf("read vault dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if len(name) >= len(restoreVaultTmpSuffix) && name[len(name)-len(restoreVaultTmpSuffix):] == restoreVaultTmpSuffix {
			t.Fatalf("leftover restore-tmp file: %s", name)
		}
	}
}

// buildValidatedRestoreArchiveFromRealVault initializes a real vault in
// dataDir via initVault and reads back its meta/vault/config bytes to build a
// validatedRestoreArchive suitable for verifyRestoredVaultUsable tests.
func buildValidatedRestoreArchiveFromRealVault(t *testing.T, dataDir, password string) *validatedRestoreArchive {
	t.Helper()
	if err := ensureDirLayout(dataDir); err != nil {
		t.Fatalf("ensure dir layout: %v", err)
	}
	if err := initVault(dataDir, password); err != nil {
		t.Fatalf("init vault: %v", err)
	}

	metaJSON, err := os.ReadFile(filepath.Join(dataDir, "vault", "meta.v1.json"))
	if err != nil {
		t.Fatalf("read meta.v1.json: %v", err)
	}
	vaultBin, err := os.ReadFile(filepath.Join(dataDir, "vault", "vault.v1.bin"))
	if err != nil {
		t.Fatalf("read vault.v1.bin: %v", err)
	}
	configBin, err := os.ReadFile(filepath.Join(dataDir, "vault", "config.v1.bin"))
	if err != nil {
		t.Fatalf("read config.v1.bin: %v", err)
	}

	return &validatedRestoreArchive{
		metaJSON:  metaJSON,
		vaultBin:  vaultBin,
		configBin: configBin,
	}
}

// TestRestoreVerifyVaultUsable covers verifyRestoredVaultUsable with a real
// vault built via initVault: correct password succeeds, wrong password is
// classified as credential mismatch.
func TestRestoreVerifyVaultUsable(t *testing.T) {
	t.Run("correct password succeeds", func(t *testing.T) {
		dataDir := t.TempDir()
		archive := buildValidatedRestoreArchiveFromRealVault(t, dataDir, "correct-horse")

		if err := verifyRestoredVaultUsable(archive, "correct-horse"); err != nil {
			t.Fatalf("unexpected error with correct password: %v", err)
		}
	})

	t.Run("wrong password is classified as credential mismatch", func(t *testing.T) {
		dataDir := t.TempDir()
		archive := buildValidatedRestoreArchiveFromRealVault(t, dataDir, "correct-horse")

		err := verifyRestoredVaultUsable(archive, "wrong-password")
		if err == nil {
			t.Fatal("expected error with wrong password")
		}
		if !errors.Is(err, errRestoreCredentialMismatch) {
			t.Fatalf("expected errRestoreCredentialMismatch classification, got: %v", err)
		}
	})
}

// TestRestoreWriteBootstrapConfig covers writeRestoredBootstrapConfig:
// success writes with correct bytes/perms; overwrite refusal leaves the
// pre-existing file's contents unchanged.
func TestRestoreWriteBootstrapConfig(t *testing.T) {
	t.Run("success writes file with correct bytes and perms", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "bootstrap.toml")
		archive := &validatedRestoreArchive{bootstrapBytes: []byte("kinko_dir=/some/path\n")}

		if err := writeRestoredBootstrapConfig(configPath, archive); err != nil {
			t.Fatalf("writeRestoredBootstrapConfig failed: %v", err)
		}

		got, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("read written config: %v", err)
		}
		if string(got) != string(archive.bootstrapBytes) {
			t.Fatalf("config content mismatch: got=%q want=%q", got, archive.bootstrapBytes)
		}
		info, err := os.Stat(configPath)
		if err != nil {
			t.Fatalf("stat written config: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("config perms=%o want=0600", perm)
		}
	})

	t.Run("overwrite is refused and pre-existing content unchanged", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "bootstrap.toml")
		originalContent := []byte("original-content\n")
		if err := os.WriteFile(configPath, originalContent, 0o600); err != nil {
			t.Fatalf("write pre-existing config: %v", err)
		}

		archive := &validatedRestoreArchive{bootstrapBytes: []byte("new-content\n")}
		err := writeRestoredBootstrapConfig(configPath, archive)
		if err == nil {
			t.Fatal("expected overwrite to be refused")
		}

		got, readErr := os.ReadFile(configPath)
		if readErr != nil {
			t.Fatalf("read config after refused overwrite: %v", readErr)
		}
		if string(got) != string(originalContent) {
			t.Fatalf("pre-existing config content changed: got=%q want=%q", got, originalContent)
		}
	})
}

// TestRestoreZipReadErrorExitCode covers zipReadErrorExitCode's mapping from
// zipReadErrorKind to CLI exit codes, plus its fallback for non-zipReadError
// errors.
func TestRestoreZipReadErrorExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"auth kind", &zipReadError{kind: zipReadErrorKindAuth}, exitCodeAuthFailed},
		{"policy kind", &zipReadError{kind: zipReadErrorKindPolicy}, exitCodePolicyFailed},
		{"io kind", &zipReadError{kind: zipReadErrorKindIO}, exitCodeIOFailed},
		{"non zip read error falls back to io", errors.New("plain error"), exitCodeIOFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := zipReadErrorExitCode(tt.err); got != tt.want {
				t.Fatalf("zipReadErrorExitCode()=%d want=%d", got, tt.want)
			}
		})
	}
}

// TestRestoreParseVaultMetaBytes covers parseVaultMetaBytes's replication of
// loadMeta's parse/version/default logic against in-memory bytes.
func TestRestoreParseVaultMetaBytes(t *testing.T) {
	t.Run("valid metadata parses with defaults applied", func(t *testing.T) {
		dataDir := t.TempDir()
		if err := ensureDirLayout(dataDir); err != nil {
			t.Fatalf("ensure dir layout: %v", err)
		}
		if err := initVault(dataDir, "pw"); err != nil {
			t.Fatalf("init vault: %v", err)
		}
		raw, err := os.ReadFile(filepath.Join(dataDir, "vault", "meta.v1.json"))
		if err != nil {
			t.Fatalf("read meta.v1.json: %v", err)
		}

		meta, err := parseVaultMetaBytes(raw)
		if err != nil {
			t.Fatalf("parseVaultMetaBytes failed: %v", err)
		}
		if meta.Version != vaultVersion {
			t.Fatalf("meta.Version=%d want=%d", meta.Version, vaultVersion)
		}
		if meta.KDFParamsPassword == nil {
			t.Fatal("expected KDFParamsPassword to be populated")
		}
	})

	t.Run("invalid JSON is rejected", func(t *testing.T) {
		_, err := parseVaultMetaBytes([]byte("not json"))
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})

	t.Run("unsupported version is rejected", func(t *testing.T) {
		_, err := parseVaultMetaBytes([]byte(`{"version":99}`))
		if err == nil {
			t.Fatal("expected error for unsupported version")
		}
	})
}
